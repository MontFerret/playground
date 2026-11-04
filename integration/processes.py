"""Linux-host process discovery and pidfd signaling for packaged tests only."""
import dataclasses
import json
import os
from pathlib import Path
import signal
import subprocess
import sys


@dataclasses.dataclass(frozen=True)
class Process:
    pid: str
    executable: str
    network: str
    parent: str
    started: str
    uids: tuple
    groups: tuple
    arguments: tuple


def decode(value):
    return Process(**{key: tuple(item) if isinstance(item, list) else item for key, item in value.items()})


def inspect(pid):
    path = Path("/proc") / str(int(pid))
    fields = dict(line.split(":", 1) for line in (path / "status").read_text().splitlines())
    stat = (path / "stat").read_text().rsplit(") ", 1)[1].split()
    return Process(str(pid), os.readlink(path / "exe"), os.readlink(path / "ns/net"), stat[1], stat[19],
                   tuple(fields["Uid"].split()), tuple(fields["Groups"].split()),
                   tuple((path / "cmdline").read_bytes().decode().rstrip("\0").split("\0")))


def host_operation(container, operation, **fields):
    # These tests require a local Linux Docker daemon. Host root can inspect
    # nondumpable workers without granting the production container extra caps.
    init = subprocess.check_output(["docker", "inspect", "--format", "{{.State.Pid}}", container], text=True).strip()
    assert int(init) > 0, "container is not running"
    privilege = [] if os.geteuid() == 0 else ["sudo", "-n"]
    result = subprocess.check_output(
        [*privilege, sys.executable, "-B", str(Path(__file__).resolve()), operation],
        input=json.dumps({"init": init, **fields}), text=True)
    return json.loads(result)


def discover(container):
    pids = subprocess.check_output(["docker", "top", container, "-eo", "pid"], text=True).split()[1:]
    records = host_operation(container, "snapshot", pids=pids)
    roles = {}
    for value in records:
        process = decode(value)
        args = process.arguments
        if args == ("/usr/bin/playground", "container"):
            role = "supervisor"
        elif args == ("/usr/bin/playground",):
            role = "api"
        elif len(args) == 3 and args[:2] == ("/usr/bin/playground", "container"):
            role = args[2]
            if role not in ("namespace", "proxy", "proxy-relay", "cdp-relay", "control-relay"):
                continue
        elif (args and args[0] == "chrome" and "--remote-debugging-port=9223" in args
              and "--user-data-dir=/tmp/playground/profile" in args
              and not any(arg.startswith("--type=") for arg in args)):
            role = "browser"
        elif process.executable == "/usr/bin/tini":
            role = "init"
        else:
            continue
        assert role not in roles, f"ambiguous process role: {role}"
        roles[role] = process
    expected = {"init": "0", "supervisor": "0", "namespace": "65530", "proxy": "65532", "api": "65532",
                "proxy-relay": "10002", "cdp-relay": "10002", "browser": "10001", "control-relay": "65532"}
    assert roles.keys() == expected.keys(), f"unexpected process roles: {roles.keys()}"
    for role, uid in expected.items():
        process = roles[role]
        assert set(process.uids) == {uid}, f"incorrect UID: {role}"
        if role == "init":
            continue
        assert not process.groups, f"inherited supplementary groups: {role}"
        assert process.executable == ("/opt/chrome-linux64/chrome" if role == "browser" else "/usr/bin/playground"), role
        parent = "namespace" if role in ("browser", "proxy-relay", "cdp-relay") else "supervisor"
        if role == "supervisor":
            parent = "init"
        assert process.parent == roles[parent].pid, f"incorrect parent: {role}"
        network = roles["namespace" if parent == "namespace" or role == "namespace" else "init"].network
        assert process.network == network, f"incorrect namespace: {role}"
    assert roles["namespace"].network != roles["init"].network, "shared browser network namespace"
    return roles


def kill_checked(container, process):
    host_operation(container, "kill", target=dataclasses.asdict(process))


def main():
    assert os.geteuid() == 0, "host procfs inspection requires root"
    payload = json.load(sys.stdin)
    init = inspect(payload["init"])
    assert init.executable == "/usr/bin/tini" and init.arguments == (
        "/usr/bin/tini", "--", "/usr/bin/playground", "container"), "unexpected container init"
    if sys.argv[1] == "snapshot":
        records = []
        for pid in payload["pids"]:
            try:
                records.append(dataclasses.asdict(inspect(pid)))
            except (FileNotFoundError, ProcessLookupError):
                continue
        print(json.dumps(records))
        return
    assert sys.argv[1] == "kill", "unknown test operation"
    expected = decode(payload["target"])
    descriptor = os.pidfd_open(int(expected.pid))
    try:
        # Open the pidfd before inspecting; even exit/PID reuse after the
        # comparison cannot redirect the signal to an unrelated process.
        assert inspect(expected.pid) == expected, "process identity changed"
        ancestor = expected
        seen = set()
        while ancestor.pid != init.pid:
            assert ancestor.pid not in seen and ancestor.parent != "0", "target left container"
            seen.add(ancestor.pid)
            ancestor = inspect(ancestor.parent)
        signal.pidfd_send_signal(descriptor, signal.SIGKILL)
    finally:
        os.close(descriptor)
    print("null")


if __name__ == "__main__":
    main()
