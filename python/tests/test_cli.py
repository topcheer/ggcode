from __future__ import annotations

import os
import unittest
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


if __name__ == "__main__":
    unittest.main()
