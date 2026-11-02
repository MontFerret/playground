"""Publication sequencing checks using a local registry-command double."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

PUBLIC = "ghcr.io/montferret/playground"
PRIVATE = "us-east1-docker.pkg.dev/montferret/playground/playground"
DIGEST = "sha256:" + "1" * 64
DOUBLE = """#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
state=Path(os.environ['TEST_REGISTRY_STATE'])
s=json.loads(state.read_text());a=sys.argv[1:]
def save():state.write_text(json.dumps(s))
if a[0]=='manifest-digest':
 print(Path(a[1]).read_text());sys.exit(0)
if a[0]=='inspect':
 ref=a[-1]
 if ref.startswith('oci-archive:'):print(os.environ['TEST_DIGEST']);sys.exit(0)
 if ref not in s['refs']:
  print('403 Forbidden' if s.get('github_first') and 'ghcr.io' in ref else 'manifest unknown',file=sys.stderr);sys.exit(1)
 print(s['refs'][ref]);sys.exit(0)
if a[0]=='copy':
 src,dst=a[-2:];s['copies'].append([src,dst]);save()
 if '--src-no-creds' in a and '/anonymous' in dst and not s.get('anonymous_allowed'):
  print('unauthorized',file=sys.stderr);sys.exit(1)
 if dst.startswith('docker://'):
  if s.get('fail')==dst:print('unavailable',file=sys.stderr);sys.exit(1)
  s['refs'][dst]=os.environ['TEST_DIGEST'];save()
 sys.exit(0)
sys.exit(2)
"""


class Publication(unittest.TestCase):
    def run_case(self, channel="stable", verified=True, fail=None, existing=None, anonymous_allowed=False, github_status=None):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            (root / "scripts/release").mkdir(parents=True)
            (root / "dist").mkdir()
            shutil.copy("scripts/release/publish.sh", root / "scripts/release/publish.sh")
            descriptor = dict(public=PUBLIC, private=PRIVATE, version="1.2.3" if channel == "stable" else "1.2.3-rc.1",
                              minor="1.2", channel=channel, digest=DIGEST, verified=verified)
            (root / "dist/image.json").write_text(json.dumps(descriptor))
            (root / "dist/playground.oci.tar").touch()
            (root / "skopeo").write_text(DOUBLE)
            (root / "skopeo").chmod(0o755)
            if github_status is not None:
                (root / "curl").write_text("#!/bin/sh\nprintf '%s' " + str(github_status) + "\n")
                (root / "curl").chmod(0o755)
            state = root / "state.json"
            state.write_text(json.dumps(dict(refs=existing or {}, copies=[], fail=fail, anonymous_allowed=anonymous_allowed, github_first=github_status is not None)))
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       TEST_REGISTRY_STATE=str(state), TEST_DIGEST=DIGEST, GH_TOKEN="local-test-token")
            result = subprocess.run(["bash", str(root / "scripts/release/publish.sh")],
                                    env=env, capture_output=True, text=True)
            return result.returncode, json.loads(state.read_text())

    def test_stable_promotes_after_full_versions(self):
        code, state = self.run_case()
        self.assertEqual(code, 0)
        destinations = [dst for _, dst in state["copies"] if dst.startswith("docker://")]
        self.assertEqual(destinations[:2], ["docker://" + x + ":1.2.3" for x in (PUBLIC, PRIVATE)])
        self.assertEqual(len(destinations), 6)

    def test_prerelease_does_not_promote(self):
        code, state = self.run_case(channel="prerelease")
        self.assertEqual(code, 0)
        self.assertEqual(len(state["refs"]), 2)

    def test_partial_failure_keeps_aliases(self):
        code, state = self.run_case(fail="docker://" + PRIVATE + ":1.2.3")
        self.assertNotEqual(code, 0)
        self.assertEqual(list(state["refs"]), ["docker://" + PUBLIC + ":1.2.3"])

    def test_recovery_reuses_matching_version(self):
        code, state = self.run_case(existing={"docker://" + PUBLIC + ":1.2.3": DIGEST})
        self.assertEqual(code, 0)
        self.assertNotIn(["oci-archive:dist/playground.oci.tar", "docker://" + PUBLIC + ":1.2.3"], state["copies"])

    def test_conflict_does_not_overwrite(self):
        code, state = self.run_case(existing={"docker://" + PUBLIC + ":1.2.3": "sha256:conflict"})
        self.assertNotEqual(code, 0)
        self.assertEqual(state["copies"], [])

    def test_first_github_package_is_confirmed_before_creation(self):
        code, _ = self.run_case(github_status=404)
        self.assertEqual(code, 0)

    def test_existing_github_package_auth_failure_stops_publication(self):
        code, state = self.run_case(github_status=200)
        self.assertNotEqual(code, 0)
        self.assertEqual(state["copies"], [])

    def test_private_registry_visibility_blocks_aliases(self):
        code, state = self.run_case(anonymous_allowed=True)
        self.assertNotEqual(code, 0)
        self.assertEqual(len(state["refs"]), 2)

    def test_unverified_artifact_cannot_publish(self):
        code, state = self.run_case(verified=False)
        self.assertNotEqual(code, 0)
        self.assertEqual(state["copies"], [])


if __name__ == "__main__":
    unittest.main()
