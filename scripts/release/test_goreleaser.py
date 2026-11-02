"""Exercise publisher dispatch and credential paths with the real GoReleaser."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class GoReleaserPublisher(unittest.TestCase):
    def test_oci_publishes_once_with_docker_credentials(self):
        config = Path(".goreleaser.yml").read_text()
        # Disable external GitHub publication. Register the same extra assets
        # directly with the publisher so its dispatch still sees all three.
        self.assertEqual(config.count("  draft: true"), 1)
        self.assertEqual(config.count("    ids: [linux]"), 1)
        config = config.replace("  draft: true", "  disable: true", 1)
        config = config.replace("    ids: [linux]", """    ids: [linux]
    extra_files:
      - glob: dist/playground.oci.tar
      - glob: dist/image.json""", 1)
        for case in ("default", "docker-config", "explicit-authfile", "empty-authfile"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as td:
                root = Path(td)
                (root / "scripts/release").mkdir(parents=True)
                (root / "cmd/playground").mkdir(parents=True)
                (root / ".goreleaser.yml").write_text(config)
                (root / "go.mod").write_text("module example.com/playground\n\ngo 1.27.0\n")
                (root / "cmd/playground/main.go").write_text(
                    "package main\nvar releaseVersion string\nfunc main() {}\n")
                (root / "scripts/release/image.sh").write_text(
                    "#!/bin/sh\nmkdir -p dist\nprintf archive > dist/playground.oci.tar\n"
                    "printf '{}' > dist/image.json\n")
                (root / "scripts/release/publish.sh").write_text(
                    "#!/bin/sh\nexec python3 - <<'PY'\n"
                    "import json,os\n"
                    "with open('publisher-calls.jsonl','a') as f:\n"
                    " f.write(json.dumps({'authfile':os.environ.get('REGISTRY_AUTH_FILE'),"
                    "'github_token':bool(os.environ.get('GH_TOKEN'))})+'\\n')\nPY\n")
                for args in (["init", "-b", "main"], ["config", "user.email", "test@example.invalid"],
                             ["config", "user.name", "Release test"], ["add", "."],
                             ["commit", "-m", "fixture"], ["tag", "v1.2.3"],
                             ["remote", "add", "origin", "https://github.com/example/playground.git"]):
                    subprocess.run(["git", *args], cwd=root, check=True, capture_output=True)
                env = dict(os.environ, GITHUB_TOKEN="local-test-token", NO_COLOR="1")
                env.pop("DOCKER_CONFIG", None)
                env.pop("REGISTRY_AUTH_FILE", None)
                expected = str(Path(env["HOME"]) / ".docker/config.json")
                if case in ("docker-config", "explicit-authfile"):
                    env["DOCKER_CONFIG"] = str(root / "docker config")
                    expected = str(Path(env["DOCKER_CONFIG"]) / "config.json")
                if case == "explicit-authfile":
                    env["REGISTRY_AUTH_FILE"] = str(root / "registry auth.json")
                    expected = env["REGISTRY_AUTH_FILE"]
                if case == "empty-authfile":
                    env["DOCKER_CONFIG"] = ""
                    env["REGISTRY_AUTH_FILE"] = ""
                result = subprocess.run(
                    ["goreleaser", "release", "--clean", "--skip=validate,announce"],
                    cwd=root, env=env, capture_output=True, text=True, timeout=120,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                calls = [json.loads(line) for line in (root / "publisher-calls.jsonl").read_text().splitlines()]
                self.assertEqual(calls, [{"authfile": expected, "github_token": True}])


if __name__ == "__main__":
    unittest.main()
