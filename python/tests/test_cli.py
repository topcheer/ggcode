from __future__ import annotations

import os
import unittest
from unittest.mock import patch

from ggcode_release_installer import cli


class ResolveTargetTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
