"""Black-box checks of the API image selected by integration/run.sh."""
import dataclasses
import http.client
import json
import os
import socket
import subprocess
import threading
import time
from pathlib import Path

from processes import discover, kill_checked


api = os.environ["PLAYGROUND_API_CONTAINER"]
fixture = os.environ["PLAYGROUND_FIXTURE_CONTAINER"]


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def api_exec(*args):
    return docker("exec", api, *args)


def api_logs():
    # Docker forwards container stderr to CLI stderr. Capture both streams so
    # redaction assertions cover the service's actual operational log stream.
    return subprocess.check_output(["docker", "logs", api], text=True, stderr=subprocess.STDOUT)


addr = docker("port", api, "8080/tcp")
origin = "https://ferretlang.org"
site = "https://mockery.ferretlang.org"
marker = "private-fql-output-53d681"
demo = Path("examples/products.fql").read_text()


def request(route, payload=None, method=None, headers=None, connection=None):
    conn = connection or http.client.HTTPConnection(addr, timeout=34)
    try:
        conn.request(method or ("GET" if payload is None else "POST"), "/api/v1/" + route,
                     json.dumps(payload) if payload is not None else None,
                     {"Content-Type": "application/json", "Origin": origin, **(headers or {})})
        res = conn.getresponse()
        body = res.read()
        assert res.headers.get("Access-Control-Allow-Origin") == origin, res.status
        return res.status, json.loads(body) if body else None
    finally:
        conn.close()


def program(text, **fields):
    return {"source": {"text": text}, **fields}


def execute(text, **fields):
    status, result = request("execute", program(text, **fields))
    assert status == 200 and result["status"] == "ok", (status, result)
    return result.get("output")


# Keep one private fixture signal process alive. Starting a new docker exec
# for every release can consume the shutdown grace period on slower runners.
agent = subprocess.Popen(
    ["docker", "exec", "-i", fixture, "python", "-u", "-c",
     "import sys,json,urllib.request\n"
     "for line in sys.stdin:\n"
     " action,token=json.loads(line)\n"
     " try:\n"
     "  data=urllib.request.urlopen('http://127.0.0.1:9000/'+action+'/'+token,timeout=15).read().decode()\n"
     "  print(json.dumps({'value':data}),flush=True)\n"
     " except Exception:\n"
     "  print(json.dumps({'error':'fixture signal failed'}),flush=True)"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
)


def control(action, token):
    agent.stdin.write(json.dumps([action, token]) + "\n")
    agent.stdin.flush()
    response = agent.stdout.readline()
    assert response, "fixture signal process exited"
    value = json.loads(response)
    assert "value" in value, value
    return value["value"]


def targets():
    return {x["id"] for x in json.loads(api_exec("curl", "-fsS", "http://127.0.0.1:9222/json/list")) if x["type"] == "page"}


def eventually(predicate, timeout=7):
    until = time.monotonic() + timeout
    while not predicate():
        assert time.monotonic() < until, "condition did not settle"
        time.sleep(0.05)


def document(path):
    return f'DOCUMENT("{site}{path}", {{driver:"cdp"}})'


def state(path):
    return (f'LET page = {document(path)}\n'
            'RETURN WAITFOR VALUE page[~? css`#state`].textContent WHEN . != "" TIMEOUT 5000')


class Held:
    def __init__(self, token):
        self.token = token
        self.conn = http.client.HTTPConnection(addr, timeout=34)
        self.result = None
        self.thread = threading.Thread(target=self.run)
        self.thread.start()
        control("wait", token)

    def run(self):
        try:
            self.result = request("execute", program(f"LET page = {document('/hold/'+self.token)} RETURN 1"),
                                  connection=self.conn)
        except (OSError, http.client.HTTPException):
            pass

    def release(self):
        control("release", self.token)
        self.thread.join(8)
        assert not self.thread.is_alive()


def checks():
    global addr

    def ready():
        try:
            return request("environment")[0] == 200
        except (OSError, http.client.HTTPException):
            return False

    eventually(ready, timeout=60)
    startup = [json.loads(line) for line in api_logs().splitlines()]
    assert any(entry.get("msg") == "Container network namespace inspection" and entry.get("distinct") is True for entry in startup)
    assert any(entry.get("msg") == "Container network inspection" and entry.get("netlinkOnlyLoopback") is True
               and entry.get("procfsOnlyLoopback") is True and entry.get("netlinkOK") is True and entry.get("procfsOK") is True for entry in startup)
    assert {entry.get("stage") for entry in startup if entry.get("msg") == "Container isolation verified"} == {
        "network_policy", "renderer_sandbox", "browser_connections"}
    bindings = json.loads(docker("inspect", "--format", "{{json .NetworkSettings.Ports}}", api))
    assert bindings["8080/tcp"][0]["HostIp"] == "127.0.0.1", bindings
    assert not bindings.get("9222/tcp"), bindings
    _, env = request("environment")
    assert env["runtime"]["modules"] == [{"name": "html", "version": "v1.0.0-rc.24"}], env
    assert env["runtime"]["version"] == "v2.0.0-alpha.61"
    if os.environ.get("EXPECTED_SERVICE_VERSION"):
        assert env["service"]["version"] == os.environ["EXPECTED_SERVICE_VERSION"], env
    assert env["limits"]["execution"]["maxConcurrentExecutions"] == 2
    assert request("format", program("RETURN    1"))[1]["formattedSource"] == "return 1"
    diag = request("analyze", program('RETURN "😀" +'))[1]["diagnostics"][0]
    assert diag["location"]["range"] == {"start":{"line":0,"character":12},"end":{"line":0,"character":13}}, diag
    artifacts = request("compile", program("RETURN @value", include=["bytecode","parameters"]))[1]["artifacts"]
    assert artifacts["parameters"] == ["value"] and artifacts["bytecode"]["text"]
    assert execute("RETURN @value", params={"value": marker}) == marker
    assert execute("LET logged = PRINT(@value) RETURN @value", params={"value": marker}) == marker
    assert execute(demo) == [{"brand":"Northwind","title":"Laptop","price":1299.5},
                             {"brand":"Contoso","title":"Mouse","price":19.99}]
    assert request("execute", method="OPTIONS", headers={"Access-Control-Request-Method":"POST",
                   "Access-Control-Request-Headers":"Content-Type"})[0] == 204
    assert request("execute", {})[0] == 400
    # Normal startup already ran the browser-UID namespace probes.
    api_exec("curl", "-fsS", "http://127.0.0.1:9222/json/version")
    api_exec("sh", "-c", "test ! -e /tmp/playground/check.sock && test ! -e /tmp/playground/processes")
    discover(api)
    baseline = targets()
    assert json.loads(execute(state("/write"))) == ["run=private","private","private"]
    assert json.loads(execute(state("/read"))) == ["","",""]
    eventually(lambda: targets() == baseline)
    # Positive control for the destination counter, independent of its hostname
    # allowlist. Afterwards neither a redirect nor a subresource may increment it.
    docker("exec",fixture,"python","-c",
            "import http.client,ssl;c=http.client.HTTPSConnection('localhost',context=ssl._create_unverified_context());"
            "c.request('GET','/control',headers={'Host':'forbidden.test'});assert c.getresponse().status==200")
    assert control("count","forbidden") == "1"
    request("execute", program(f"LET p = {document('/redirect')} RETURN p.url"))
    assert execute(state("/subresource")) == "blocked"
    assert control("count","forbidden") == "1"
    for query in ['RETURN DOWNLOAD("http://127.0.0.1:9222/json")',
                  'RETURN DOCUMENT("file:///etc/passwd")', 'RETURN DOCUMENT("chrome://version")',
                  'RETURN DOCUMENT("http://169.254.169.254/")', 'RETURN DOCUMENT("http://127.0.0.1:9222/json")',
                  f'RETURN DOCUMENT("{site}/",{{keepCookies:true}})']:
        status, body = request("execute", program(query))
        assert status == 200 and body["status"] == "failed" and "output" not in body, body
    a, b = Held("one"), Held("two")
    assert request("environment")[0] == 200
    before = time.monotonic()
    status, body = request("execute", program("RETURN 0"))
    assert status == 429 and body["error"]["code"] == "execution_capacity_exceeded"
    assert time.monotonic() - before < 1
    a.conn.sock.shutdown(socket.SHUT_RDWR)
    a.release()
    eventually(lambda: request("execute",program("RETURN 7"))[0] == 200)
    b.release()
    assert b.result[1].get("output") == 1, b.result
    eventually(lambda: targets() == baseline)
    for query, options, code in [(f"LET p = {document('/hold/deadline')} RETURN 1", {"timeoutMs":500}, "execution_timeout"),
                                 ('RETURN REPEAT("a",1100000)', {}, "output_too_large")]:
        status, body = request("execute", program(query, options=options))
        assert status == 200 and body["status"] == "failed" and "output" not in body and body["diagnostics"][0]["code"] == code, body
    control("release","deadline")
    assert marker not in api_logs()

    shutdowns = []
    for force in (False, True):
        active = Held("forced" if force else "graceful")
        cid = api
        before = time.monotonic()
        signaled = subprocess.Popen(["docker", "kill", "--signal=SIGTERM", cid],
                                    stdout=subprocess.DEVNULL)
        if not force:
            def stopped_admission():
                try:
                    status, _ = request("environment")
                    return status == 503
                except (OSError, http.client.HTTPException):
                    return True

            eventually(stopped_admission, timeout=3)
            active.release()
            assert active.result is not None and active.result[1].get("output") == 1, active.result
        signaled.wait(timeout=10)
        exitcode = subprocess.check_output(["docker","wait",cid],text=True,timeout=10).strip()
        elapsed = time.monotonic()-before
        assert exitcode == ("1" if force else "0") and elapsed < 9, (exitcode,elapsed)
        if force:
            assert elapsed >= 3.8
            active.release()
        assert docker("inspect", "--format", "{{.State.Pid}}", api) == "0"
        print(f"shutdown force={force} exit={exitcode} elapsed={elapsed:.2f}s", flush=True)
        shutdowns.append({"forced": force, "exit": int(exitcode), "elapsedSeconds": round(elapsed, 3)})
        if not force:
            docker("start", api)
            addr = docker("port", api, "8080/tcp")
            eventually(ready, timeout=60)
    for role in ("browser", "proxy-relay"):
        docker("start", api)
        addr = docker("port", api, "8080/tcp")
        eventually(ready, timeout=60)
        assert json.loads(execute(state("/read"))) == ["", "", ""]
        child = discover(api)[role]
        try:
            kill_checked(api, dataclasses.replace(child, started="0"))
        except subprocess.CalledProcessError:
            pass
        else:
            raise AssertionError("failure injection accepted a changed process identity")
        before = time.monotonic()
        kill_checked(api, child)
        code = subprocess.check_output(["docker", "wait", api], text=True, timeout=10).strip()
        assert code != "0" and time.monotonic() - before < 9, (role, code)
        assert docker("inspect", "--format", "{{.State.Pid}}", api) == "0"
        assert marker not in api_logs()
    resources = json.loads(docker("inspect", "--format", "{{json .HostConfig}}", api))
    print("Verification evidence: " + json.dumps({
        "runner": docker("info", "--format", "{{.OperatingSystem}} {{.Architecture}} {{.KernelVersion}}"),
        "runnerCPUs": int(docker("info", "--format", "{{.NCPU}}")),
        "runnerMemoryBytes": int(docker("info", "--format", "{{.MemTotal}}")),
        "containerCPUs": resources["NanoCpus"] / 1e9,
        "containerMemoryBytes": resources["Memory"], "sharedMemoryBytes": resources["ShmSize"],
        "shutdowns": shutdowns,
    }), flush=True)
    print("Packaged contract, browser, isolation, admission, rollback and shutdown checks passed.")


if __name__ == "__main__":
    try:
        checks()
    finally:
        agent.stdin.close()
        try:
            agent.wait(timeout=3)
        except subprocess.TimeoutExpired:
            agent.kill()
            agent.wait()

