"""Exercise fail-closed startup through the packaged image's normal entrypoint."""
import json
import socket
import subprocess
import sys
import time
from pathlib import Path


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True, stderr=subprocess.STDOUT, timeout=12).strip()


def comparison(index, operation, value, value_two=0):
    return {"index": index, "op": "SCMP_CMP_" + operation, "value": value, "valueTwo": value_two}


def restricted_profile(replacements):
    profile = json.loads(Path("deploy/browser/seccomp.json").read_text())
    rules = []
    for rule in profile["syscalls"]:
        names = [name for name in rule["names"] if name not in replacements]
        if names:
            rules.append(dict(rule, names=names))
    for name, predicates in replacements.items():
        rules.extend({"names": [name], "action": "SCMP_ACT_ALLOW", "args": args} for args in predicates)
    profile["syscalls"] = rules
    return profile


def failures(directory):
    for case, bit, stages, predecessor in (
        ("namespace", 0x10000000, {"namespace_start_failed"}, None),
        ("renderer", 0x20000000, {"chromium_exited", "renderer_sandbox_unavailable"}, "network_policy"),
    ):
        allow = [[comparison(0, "MASKED_EQ", bit)]]
        # Keep clone3's ordinary ENOSYS fallback. Its flags live in memory,
        # while the pinned browser and Go can both use the clone fallback.
        profile = restricted_profile({"clone": allow, "unshare": allow, "clone3": []})
        profile["syscalls"].append({"names": ["clone3"], "action": "SCMP_ACT_ERRNO", "errnoRet": 38})
        yield case, stages, predecessor, profile, []
    for case, protocol, stage in (("route", 0, "network_interface_enumeration_failed"),
                                  ("netfilter", 12, "namespace_policy_unavailable")):
        profile = restricted_profile({"socket": [[comparison(0, "NE", 16)],
                                                 [comparison(0, "EQ", 16), comparison(2, "NE", protocol)]]})
        yield case, {stage}, None, profile, []
    profile = restricted_profile({"socket": [[comparison(0, "NE", socket.AF_INET)]]})
    yield "control", {"control_listener_failed"}, None, profile, []
    nft = directory / "nft"
    nft.write_text('''#!/bin/sh
set -eu
/usr/sbin/nft "$@"
exec /usr/sbin/nft insert rule inet playground output meta skuid 10001 ip daddr 127.0.0.1 tcp dport 3128 drop
''')
    nft.chmod(0o755)
    yield "mandatory-probe", {"network_isolation_unavailable"}, "renderer_sandbox", restricted_profile({}), [
        "--volume", f"{nft}:/usr/local/sbin/nft:ro"]


def check(directory, image, container, case, expected, predecessor, profile, mounts):
    profile_file = directory / f"{case}.json"
    profile_file.write_text(json.dumps(profile))
    with socket.socket() as reserve:
        reserve.bind(("127.0.0.1", 0))
        port = reserve.getsockname()[1]
    docker("create", "--name", container, "--pull", "never", "--platform", "linux/amd64",
           "--read-only", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=1073741824",
           "--security-opt", "no-new-privileges:true", "--security-opt", f"seccomp={profile_file}",
           "--cap-drop", "ALL", "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "CHOWN",
           "--cap-add", "KILL", "--cap-add", "DAC_OVERRIDE", "--network", "bridge",
           "--publish", f"127.0.0.1:{port}:8080", *mounts, image)
    docker("start", container)
    waiter = subprocess.Popen(["docker", "wait", container], text=True, stdout=subprocess.PIPE)
    try:
        deadline = time.monotonic() + 50
        while waiter.poll() is None:
            assert time.monotonic() < deadline, f"startup fault timed out: {case}"
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.1) as client:
                    client.settimeout(0.1)
                    client.sendall(b"GET /api/v1/environment HTTP/1.1\r\nHost: localhost\r\n\r\n")
                    assert not client.recv(256).startswith(b"HTTP/"), f"HTTP opened during {case}"
            except OSError:
                pass
            try:
                waiter.wait(timeout=0.1)
            except subprocess.TimeoutExpired:
                continue
        exitcode = waiter.stdout.read().strip()
        logs = docker("logs", container)
        events = [json.loads(line) for line in logs.splitlines() if line.startswith("{")]
        stages = {event.get("stage") or event.get("code") for event in events}
        assert waiter.returncode == 0 and exitcode == "1" and expected & stages, logs
        assert predecessor is None or predecessor in stages, logs
        assert not any(event.get("msg") in ("Service listening", "Container cleanup failed", "Namespace cleanup failed")
                       for event in events), logs
        cleanup, = [event for event in events if event.get("msg") == "Container cleanup finished"]
        assert cleanup["elapsedMs"] <= 8500, cleanup
        state = json.loads(docker("inspect", "--format", "{{json .State}}", container))
        assert state["Pid"] == 0 and not state["OOMKilled"] and state.get("Health", {}).get("Status") != "healthy", state
    finally:
        if waiter.poll() is None:
            waiter.kill()
            waiter.wait()
        waiter.stdout.close()
    docker("rm", "--volumes", container)
    print(f"Verified fail-closed startup: {case}", flush=True)


if __name__ == "__main__":
    directory, image, container = sys.argv[1:]
    directory = Path(directory)
    for fault in failures(directory):
        check(directory, image, container, *fault)
