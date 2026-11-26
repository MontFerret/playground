"""Linux-host process discovery and pidfd signaling for packaged tests only."""
import dataclasses
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
from contextlib import contextmanager


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
    gids: tuple
    namespace_pids: tuple
    seccomp: str
    filters: int
    no_new_privs: str


def decode(value):
    return Process(**{key: tuple(item) if isinstance(item, list) else item for key, item in value.items()})


def inspect(pid):
    path = Path("/proc") / str(int(pid))
    fields = dict(line.split(":", 1) for line in (path / "status").read_text().splitlines())
    stat = (path / "stat").read_text().rsplit(") ", 1)[1].split()
    return Process(str(pid), os.readlink(path / "exe"), os.readlink(path / "ns/net"), stat[1], stat[19],
                   tuple(fields["Uid"].split()), tuple(fields["Groups"].split()),
                   tuple((path / "cmdline").read_bytes().decode().rstrip("\0").split("\0")),
                   tuple(fields["Gid"].split()), tuple(fields["NSpid"].split()), fields["Seccomp"].strip(),
                   int(fields["Seccomp_filters"]), fields["NoNewPrivs"].strip())


def container_init(container):
    # These tests require a local Linux Docker daemon. Host root can inspect
    # nondumpable workers without granting the production container extra caps.
    init = subprocess.check_output(["docker", "inspect", "--format", "{{.State.Pid}}", container], text=True).strip()
    assert int(init) > 0, "container is not running"
    return init


def privileged(*command):
    return list(command) if os.geteuid() == 0 else ["sudo", "-n", *command]


def host_operation(container, operation, **fields):
    init = container_init(container)
    result = subprocess.check_output(
        privileged(sys.executable, "-B", str(Path(__file__).resolve()), operation),
        input=json.dumps({"init": init, **fields}), text=True, timeout=10)
    return json.loads(result)


def namespace_probe(worker, uid, gid, mode):
    return privileged("nsenter", "--target", worker.pid, "--user", "--net", "--pid", "--root",
                      "--setuid", str(uid), "--setgid", str(gid), "/usr/local/bin/browser-state", "probe", mode)


@contextmanager
def observe_socket_cleanup(container):
    # Keep the tmpfs directory open while Tini exits. Checking the original
    # directory still distinguishes unlink from Docker discarding the mount.
    watcher = subprocess.Popen(privileged(sys.executable, "-B", str(Path(__file__).resolve()),
                                          "socket-cleanup", container_init(container)),
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
    try:
        assert watcher.stdout.readline().strip() == "ready", "socket cleanup witness unavailable"

        def assert_removed():
            output, _ = watcher.communicate("check\n", timeout=5)
            assert watcher.returncode == 0 and output.strip() == "removed", output

        yield assert_removed
    finally:
        if watcher.poll() is None:
            watcher.stdin.close()
            watcher.wait(timeout=5)
        watcher.stdout.close()


def snapshot(container):
    pids = subprocess.check_output(["docker", "top", container, "-eo", "pid"], text=True).split()[1:]
    return [decode(value) for value in host_operation(container, "snapshot", pids=pids)]


def assert_reaped(processes):
    for process in processes:
        try:
            stat = (Path("/proc") / process.pid / "stat").read_text().rsplit(") ", 1)[1].split()
        except (FileNotFoundError, ProcessLookupError):
            continue
        assert stat[19] != process.started, f"container process {process.pid} survived shutdown"


def discover(container):
    roles = {}
    for process in snapshot(container):
        args = process.arguments
        if args == ("/usr/bin/playground", "container"):
            role = "supervisor"
        elif args == ("/usr/bin/playground",):
            role = "api"
        elif len(args) == 3 and args[:2] == ("/usr/bin/playground", "container"):
            role = args[2]
            if role not in ("namespace", "proxy", "proxy-relay", "cdp-relay"):
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
                "proxy-relay": "10002", "cdp-relay": "10002", "browser": "10001"}
    assert roles.keys() == expected.keys(), f"unexpected process roles: {roles.keys()}"
    for role, uid in expected.items():
        process = roles[role]
        assert set(process.uids) == {uid}, f"incorrect UID: {role}"
        if role == "init":
            continue
        expected_gid = "65532" if role in ("cdp-relay", "proxy-relay") else uid
        assert set(process.gids) == {expected_gid}, f"incorrect GID: {role}"
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
    operation = sys.argv[1]
    payload = {"init": sys.argv[2]} if operation == "socket-cleanup" else json.load(sys.stdin)
    init = inspect(payload["init"])
    assert init.executable == "/usr/bin/tini" and init.arguments == (
        "/usr/bin/tini", "--", "/usr/bin/playground", "container"), "unexpected container init"
    if operation == "socket-cleanup":
        descriptor = os.open(f"/proc/{init.pid}/root/tmp/playground", os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.stat("egress.sock", dir_fd=descriptor)
            print("ready", flush=True)
            if sys.stdin.readline().strip() == "check":
                try:
                    os.stat("egress.sock", dir_fd=descriptor)
                except FileNotFoundError:
                    print("removed", flush=True)
                else:
                    raise AssertionError("egress socket survived shutdown")
        finally:
            os.close(descriptor)
        return
    if operation == "snapshot":
        records = []
        for pid in payload["pids"]:
            try:
                records.append(dataclasses.asdict(inspect(pid)))
            except (FileNotFoundError, ProcessLookupError):
                continue
        print(json.dumps(records))
        return
    assert operation == "kill", "unknown test operation"
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
