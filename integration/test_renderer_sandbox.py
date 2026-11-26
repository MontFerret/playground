"""Regression cases for the packaged suite's live renderer assertions."""
import dataclasses
import subprocess
import unittest
from unittest.mock import patch

from processes import Process
from security import assert_renderer_sandbox


class RendererSandboxTest(unittest.TestCase):
    def setUp(self):
        browser_ids = ("10001",) * 4
        self.renderer = Process(pid="301", executable="chrome", network="renderer-net", parent="300",
                                started="1", uids=browser_ids, gids=browser_ids, groups=(),
                                arguments=("chrome", "--type=renderer"), namespace_pids=("301", "31", "2"),
                                seccomp="2", filters=2, no_new_privs="1")
        self.worker = dataclasses.replace(self.renderer, pid="200", network="worker-net",
                                          namespace_pids=("200", "10"), filters=1)
        self.layout = {"interfaces": ["lo"], "-4": [], "-6": []}
        observer = patch("security.renderer_network", return_value=self.layout)
        self.inspect = observer.start()
        self.addCleanup(observer.stop)

    def check(self, **changes):
        renderer = dataclasses.replace(self.renderer, **changes)
        with patch("security.snapshot", return_value=[self.worker, renderer]):
            assert_renderer_sandbox("api", self.worker, "parent-net", "chrome", [{"type": "renderer", "id": 31}])

    def test_verified_worker_namespace(self):
        self.check(network=self.worker.network)
        self.inspect.assert_not_called()

    def test_separate_loopback_namespace(self):
        self.layout["-4"] = [{"dst": "127.0.0.0/8", "type": "local"}]
        self.layout["-6"] = [{"dst": "::/0", "type": "unreachable"}]
        self.check()
        self.inspect.assert_called_once_with(self.renderer)

    def test_parent_namespace(self):
        with self.assertRaisesRegex(AssertionError, "parent network namespace"):
            self.check(network="parent-net")
        self.inspect.assert_not_called()

    def test_renderer_process_title(self):
        self.check(arguments=("chrome --type=renderer",))
        for arguments in (("chrome", "--no-sandbox"), ("chrome --type=renderer --no-sandbox",)):
            with self.subTest(arguments=arguments), self.assertRaisesRegex(AssertionError, "disables sandbox"):
                self.check(arguments=arguments)
        with self.assertRaisesRegex(AssertionError, "renderer executable"):
            self.check(executable="other")

    def test_insufficient_pid_depth(self):
        with self.assertRaisesRegex(AssertionError, "depth 2, worker depth 2"):
            self.check(namespace_pids=("301", "31"))

    def test_external_or_missing_interface(self):
        for interfaces in (["lo", "eth0"], []):
            with self.subTest(interfaces=interfaces), self.assertRaisesRegex(AssertionError, "only loopback"):
                self.layout["interfaces"] = interfaces
                self.check()

    def test_usable_default_route(self):
        for family, dst in (("-4", "default"), ("-6", "::/0")):
            with self.subTest(family=family), self.assertRaisesRegex(AssertionError, "default route"):
                self.layout[family] = [{"dst": dst, "table": 100}]
                self.check()
            self.layout[family] = []

    def test_unavailable_inspection(self):
        self.inspect.side_effect = subprocess.CalledProcessError(1, "nsenter")
        with self.assertRaises(subprocess.CalledProcessError):
            self.check()


if __name__ == "__main__":
    unittest.main()
