from __future__ import annotations

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from ggcode_release_installer import cli


class ResolveTargetTests(unittest.TestCase):
    def test_normalize_version_defaults_to_latest(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(cli.normalize_version(), "latest")

    def test_normalize_version_uses_explicit_override(self) -> None:
        with patch.dict(os.environ, {"GGCODE_INSTALL_VERSION": "1.2.3"}, clear=True):
            self.assertEqual(cli.normalize_version(), "v1.2.3")

    def test_resolve_target_uses_platform_module_for_machine(self) -> None:
        with patch.object(cli.sys, "platform", "darwin"):
            with patch.object(cli.platform, "machine", return_value="arm64"):
                self.assertEqual(cli.resolve_target(), ("darwin", "arm64"))

    def test_resolve_target_falls_back_to_processor_architecture(self) -> None:
        env = dict(os.environ, PROCESSOR_ARCHITECTURE="AMD64")
        with patch.dict(os.environ, env, clear=True):
            with patch.object(cli.sys, "platform", "win32"):
                with patch.object(cli.platform, "machine", return_value=""):
                    self.assertEqual(cli.resolve_target(), ("windows", "x86_64"))

    def test_resolve_release_version_follows_latest_redirect(self) -> None:
        response = unittest.mock.Mock()
        response.geturl.return_value = "https://github.com/topcheer/ggcode/releases/tag/v9.9.9"
        response.__enter__ = unittest.mock.Mock(return_value=response)
        response.__exit__ = unittest.mock.Mock(return_value=False)
        with patch.object(cli.urllib.request, "urlopen", return_value=response):
            self.assertEqual(cli.resolve_release_version("latest"), "v9.9.9")


class PathHandlingTests(unittest.TestCase):
    def test_preferred_install_dirs_unix(self) -> None:
        with patch.object(cli, "os") as fake_os:
            fake_os.name = "posix"
            with patch.object(cli.Path, "home", return_value=Path("/home/tester")):
                dirs = cli.preferred_install_dirs()
        self.assertEqual(dirs, [Path("/usr/local/bin"), Path("/home/tester/.local/bin")])

    def test_upsert_path_block_appends_marker(self) -> None:
        updated = cli.upsert_path_block("export FOO=bar\n", Path("/tmp/ggcode-bin"))
        self.assertIn(cli.MARKER_START, updated)
        self.assertIn('export PATH="/tmp/ggcode-bin:$PATH"', updated)

    def test_upsert_path_block_replaces_existing_marker(self) -> None:
        original = (
            f"{cli.MARKER_START}\n"
            'export PATH="/old/bin:$PATH"\n'
            f"{cli.MARKER_END}\n"
        )
        updated = cli.upsert_path_block(original, Path("/new/bin"))
        self.assertNotIn("/old/bin", updated)
        self.assertIn('export PATH="/new/bin:$PATH"', updated)

    def test_existing_install_uses_binary_without_resolving_latest(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            install_dir = Path(temp_dir)
            binary_path = install_dir / "ggcode"
            binary_path.write_text("stub", encoding="utf-8")
            cli.write_metadata(install_dir, "v1.0.21")

            with patch.object(cli, "preferred_install_dirs", return_value=[install_dir]):
                with patch.object(cli, "ensure_installed_path", return_value=False):
                    result = cli.existing_install("latest", "ggcode")

        self.assertIsNotNone(result)
        assert result is not None
        self.assertEqual(result.binary_path, binary_path)
        self.assertEqual(result.version, "v1.0.21")

    def test_install_binary_writes_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            temp_root = Path(temp_dir)
            source = temp_root / "source"
            source.write_text("ggcode", encoding="utf-8")
            destination = temp_root / "bin" / "ggcode"

            installed = cli.install_binary(source, destination, "v9.9.9")

            self.assertEqual(installed, destination)
            self.assertEqual(destination.read_text(encoding="utf-8"), "ggcode")
            self.assertEqual(cli.read_metadata(destination.parent), {"version": "v9.9.9"})


if __name__ == "__main__":
    unittest.main()
