"""Network, renderer and control checks of the unmodified packaged entrypoint."""
import json
import subprocess
from contextlib import contextmanager
from pathlib import Path

from fixture import approved_ip, private_ip, private_ipv6
from processes import discover, namespace_probe, renderer_network, snapshot


def read_probe(command, requests=None):
    output = subprocess.check_output(command, input=json.dumps(requests) if requests is not None else "",
                                     text=True, timeout=18)
    return json.loads(output)


def dial(address, network="tcp", payload=""):
    return {"Address": address, "Network": network, "Payload": payload}


def connect_request(authority, method="CONNECT"):
    return dial("127.0.0.1:3128", payload=f"{method} {authority} HTTP/1.1\r\nHost: {authority}\r\n\r\n")


def status(result):
    assert result["Connected"], result
    protocol, code, *_ = result["Reply"].split()
    assert protocol == "HTTP/1.1", result
    return int(code)


def assert_renderer_sandbox(api, worker, parent_network, renderer_processes):
    level = len(worker.namespace_pids)
    processes = {int(p.namespace_pids[level - 1]): p for p in snapshot(api) if len(p.namespace_pids) >= level}
    renderers = {int(p["id"]) for p in renderer_processes if p["type"] == "renderer"}
    assert renderers, "CDP reported no live renderer"
    for pid in renderers:
        renderer = processes[pid]
        assert set(renderer.uids) == set(renderer.gids) == {"10001"} and not renderer.groups, renderer.pid
        depth = len(renderer.namespace_pids)
        assert depth > level, f"renderer {renderer.pid} PID namespace depth {depth}, worker depth {level}"
        assert renderer.network != parent_network, f"renderer {renderer.pid} shares the parent network namespace"
        assert renderer.seccomp == "2" and renderer.filters > worker.filters and renderer.no_new_privs == "1", renderer.pid
        assert "--type=renderer" in renderer.arguments and "--no-sandbox" not in renderer.arguments, renderer.pid
        if renderer.network != worker.network:
            layout = renderer_network(renderer)
            assert layout["interfaces"] == ["lo"], f"renderer {renderer.pid} requires only loopback"
            for family, prefix in (("-4", "0.0.0.0/0"), ("-6", "::/0")):
                routes = layout[family]
                assert isinstance(routes, list), f"invalid ip {family} route inventory for renderer {renderer.pid}"
                for route in routes:
                    if route["dst"] in ("default", prefix):
                        # These kernel route types cannot forward packets.
                        assert route.get("type") in ("unreachable", "blackhole", "prohibit", "throw"), (
                            f"renderer {renderer.pid} has a usable IPv{family[1:]} default route")


@contextmanager
def loopback_canary(command):
    with subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True) as process:
        try:
            port = json.loads(process.stdout.readline())

            def accepted():
                process.stdin.write("count\n")
                process.stdin.flush()
                return json.loads(process.stdout.readline())

            yield port, accepted
        finally:
            process.stdin.close()
            process.wait(timeout=5)
            assert process.returncode == 0, "loopback canary failed"


def verify(api, fixture, docker, control, evaluate):
    roles = discover(api)
    worker = roles["namespace"]
    parent = ["docker", "exec", "-i", "--user", "65532:65532", api, "/usr/local/bin/browser-state"]
    browser_probe = lambda mode: namespace_probe(worker, 10001, 10001, mode)
    helper_probe = lambda mode: namespace_probe(worker, 10002, 65532, mode)
    fields = read_probe(browser_probe("inspect"))
    identity = dict(line.split(":", 1) for line in fields["status"].splitlines())
    assert identity["Uid"].split() == identity["Gid"].split() == ["10001"] * 4, identity["Uid"]
    assert not identity["Groups"].strip() and int(identity["CapEff"], 16) == 0, identity["Groups"]
    assert fields["stateWriteDenied"], "browser UID can create files beside the relay socket"
    assert fields["network"] == worker.network and [i["Name"] for i in fields["interfaces"]] == ["lo"], fields["interfaces"]
    assert not any(row.split()[1] == "00000000" for row in fields["ipv4"].splitlines()[1:]), fields["ipv4"]
    for row in fields["ipv6"].splitlines():
        address, prefix, *_, flags, interface = row.split()
        if prefix == "00":
            # Linux can expose a synthetic rejecting ::/0 route for lo.
            assert address == "0" * 32 and (int(flags, 16) & 0x200 or not int(flags, 16) & 1), (flags, interface)

    inspect = json.loads(docker("exec", api, "browser-state"))
    assert_renderer_sandbox(api, worker, roles["init"].network, inspect["processes"])
    assert "--no-sandbox" not in roles["browser"].arguments
    assert docker("exec", api, "stat", "-c", "%u:%g %a %F", "/tmp/playground/egress.sock") == "10002:65532 660 socket"
    assert docker("exec", api, "stat", "-c", "%u:%g %a", "/tmp/playground") == "65530:65532 755"
    assert docker("exec", api, "stat", "-c", "%u:%g %a", "/tmp/playground/profile") == "10001:10001 700"
    listeners = docker("exec", api, "cat", "/proc/net/tcp", "/proc/net/tcp6")
    cdp = [row.split()[1] for row in listeners.splitlines() if len(row.split()) > 3 and
           row.split()[1].endswith(":2407") and row.split()[3] == "0A"]
    assert cdp == ["0100007F:2407"], cdp

    bridge_cdp(api, fixture, docker)
    with loopback_canary([*parent, "probe", "canary"]) as (outer_port, outer_count), \
         loopback_canary(helper_probe("canary")) as (loopback_port, loopback_count):
        destinations = [(approved_ip, 80), (approved_ip, 443), (approved_ip, 8081), (approved_ip, 8443),
                        (private_ip, 80), (private_ip, 443), (private_ipv6, 443)]
        counters = [f"tcp-{ip}-{port}" for ip, port in destinations]
        direct = [dial(f"[{ip}]:{port}" if ":" in ip else f"{ip}:{port}") for ip, port in destinations]
        dns = dial(f"{approved_ip}:53", "udp", "query")
        outer = dial(f"127.0.0.1:{outer_port}", payload="canary")
        local = dial(f"127.0.0.1:{loopback_port}", payload="canary")
        private_socket = "/tmp/playground/egress.sock"
        proxy_denial = "CONNECT forbidden.test:443 HTTP/1.1\r\nHost: forbidden.test:443\r\n\r\n"
        initial = counts(control, counters)
        assert control("dns", "public") == "configured"
        permitted = read_probe([*parent, "probe", "connect"], [*direct, dns, outer])
        assert all(result["Connected"] for result in permitted), permitted
        assert permitted[-2]["Reply"] == approved_ip and permitted[-1]["Reply"] == "canary", permitted
        baseline = {key: int(control("after", f"{key}={old}")) for key, old in initial.items()}
        trusted = read_probe(helper_probe("connect"), [local, dial("127.0.0.1:9223"), dial(private_socket, "unix", proxy_denial)])
        assert trusted[0]["Reply"] == "canary" and trusted[1]["Connected"] and status(trusted[2]) == 403, trusted
        assert outer_count() == loopback_count() == 1
        dns_before = int(control("count", "dns-queries"))
        forbidden = [*direct, outer, local, dial("127.0.0.1:9223"), dial("[::1]:9223"), dial(private_socket, "unix")]
        results = read_probe(browser_probe("connect"), [*forbidden, dns, connect_request("forbidden.test:443")])
        assert not any(result["Connected"] for result in results[:-2]), results
        assert results[-3]["Denied"] and results[-2]["Reply"] == "" and status(results[-1]) == 403, results
        assert int(control("count", "dns-queries")) == dns_before and outer_count() == loopback_count() == 1
        assert counts(control, counters) == baseline

        proxy_checks(browser_probe("connect"), [*parent, "probe", "connect"], control, counters, dns)
        websocket_checks(evaluate, control, counters, loopback_port, loopback_count)
    print("Verified namespace interfaces and routes, live renderer sandbox, UID controls and private relays.", flush=True)


def bridge_cdp(api, fixture, docker):
    net = json.loads(docker("inspect", "--format", "{{json .NetworkSettings.Networks}}", api))
    fixture_net = json.loads(docker("inspect", "--format", "{{json .NetworkSettings.Networks}}", fixture))
    shared = [net[name] for name in net.keys() & fixture_net.keys()]
    addresses = [{"v4": entry["IPAddress"], "v6": entry["GlobalIPv6Address"]} for entry in shared]
    assert any(address["v4"] for address in addresses), "fixture must share a parent-side network"
    docker("exec", fixture, "python", "-c", """
import json, socket, sys
for bridge in json.loads(sys.argv[1]):
    if bridge["v4"]:
        socket.create_connection((bridge["v4"], 8080), timeout=2).close()
    for address in filter(None, bridge.values()):
        try:
            socket.create_connection((address, 9223), timeout=0.7).close()
        except OSError:
            continue
        raise AssertionError('CDP listener exposed on bridge')
""", json.dumps(addresses))


def counts(control, keys):
    return {key: int(control("count", key)) for key in keys}


def proxy_checks(browser, parent, control, counters, dns):
    approved = "mockery.ferretlang.org:443"
    allow = connect_request(approved)
    assert control("dns", "public") == "configured"
    before = counts(control, counters)
    denied = [connect_request("https://mockery.ferretlang.org/", "GET"), connect_request("forbidden.test:443"),
              connect_request("outside-proxy.test:443"), connect_request("mockery.ferretlang.org:8443"),
              connect_request(f"{private_ip}:443"), connect_request(f"[{private_ipv6}]:443"),
              connect_request("mockery.ferretlang.org"), connect_request(approved + ":443"),
              connect_request("/mockery.ferretlang.org:443"), connect_request("https://" + approved)]
    assert all(status(result) in (400, 403) for result in read_probe(browser, denied))
    assert counts(control, counters) == before and int(control("count", "dns-queries")) == 0
    for mode in ("private", "mixed"):
        assert control("dns", mode) == "configured"
        rejected, = read_probe(browser, [allow])
        assert status(rejected) == 403 and int(control("count", "dns-queries")) == 1
        assert counts(control, counters) == before

    assert control("dns", "rebind") == "configured"
    accepted, = read_probe(browser, [allow])
    key = f"tcp-{approved_ip}-443"
    assert status(accepted) == 200 and int(control("after", f"{key}={before[key]}")) == before[key] + 1
    before[key] += 1
    assert counts(control, counters) == before and int(control("count", "dns-queries")) == 1
    second_answer, = read_probe(parent, [dns])
    assert second_answer["Reply"] == private_ip and int(control("count", "dns-queries")) == 2
    assert control("dns", "public") == "configured"
    print("Verified packaged CONNECT authority checks, mixed DNS denial and numeric dialing.", flush=True)


def websocket_checks(evaluate, control, counters, loopback_port, loopback_count):
    requests = {"ws": ["forbidden.test", "mockery.ferretlang.org:8081", private_ip,
                       f"127.0.0.1:{loopback_port}", "outside-proxy.test"],
                "wss": ["forbidden.test", "mockery.ferretlang.org:8443", private_ip,
                        f"127.0.0.1:{loopback_port}", "outside-proxy.test"]}
    cases = {f"{scheme}-{i}": f"{scheme}://{target}/canary/{scheme}-{i}"
             for scheme, targets in requests.items() for i, target in enumerate(targets)}
    keys = [*counters, *cases, "fetch", "xhr", "image", "script", "cross-origin-frame", "worker-fetch"]
    before = counts(control, keys)
    assert all(before[key] == 0 for key in keys if key not in counters), before
    script = Path(__file__).with_name("network.js").read_text()
    args = json.dumps({"sockets": list(cases.values()), "origin": "https://forbidden.test"})
    outcomes = evaluate("new", f"({script})({args})")
    assert outcomes.keys() == {"main", "frame", "worker"}, outcomes
    for role, events in outcomes.items():
        if role == "worker" and isinstance(events, dict):
            assert events.get("unavailable"), events
            print(f"Worker network coverage unavailable: {events['unavailable']}", flush=True)
            continue
        assert len(events) == len(cases) and all(event in ("error", "SecurityError") for event in events), (role, events)
        assert "error" in events[len(requests["ws"]):], f"{role}: no WSS attempt reached asynchronous handling"
        preflight = [case for case, outcome in zip(cases, events, strict=True) if outcome == "SecurityError"]
        if preflight:
            print(f"Chromium blocked {role} WebSockets before dispatch: {', '.join(preflight)}", flush=True)
        if "error" not in events[:len(requests["ws"])]:
            print(f"{role} ws:// transport coverage unavailable because Chromium blocked every attempt before dispatch.", flush=True)
    assert counts(control, keys) == before and loopback_count() == 1
    print("Verified WebSocket and other browser-request canaries with CDP Fetch absent.", flush=True)
