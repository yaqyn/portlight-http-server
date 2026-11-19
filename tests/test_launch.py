"""Exercise the launcher contract without Docker or a running service."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class LauncherTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.calls = self.root / 'calls'
        self.env = dict(os.environ, PATH=f'{self.root}:{os.environ["PATH"]}',
                        CALLS=str(self.calls), HOST_PORT='8991', CURL_STATUS='0')
        self.command('docker', 'printf "docker %s\\n" "$*" >> "$CALLS"\n')
        self.command('curl', 'printf "curl %s\\n" "$*" >> "$CALLS"\nexit "$CURL_STATUS"\n')

    def command(self, name, body):
        path = self.root / name
        path.write_text('#!/bin/bash\n' + body)
        path.chmod(0o755)

    def launch(self, *args):
        return subprocess.run([str(ROOT / 'launch'), *args], cwd=self.root,
                              env=self.env, text=True, capture_output=True)

    def test_check_does_not_start_containers(self):
        self.command('docker', 'exit 99\n')
        result = self.launch('check')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Ready: http://127.0.0.1:8991/readyz', result.stdout)
        calls = self.calls.read_text()
        self.assertIn('--max-time 2 http://127.0.0.1:8991/readyz', calls)
        self.assertNotIn('docker ', calls)

    def test_check_failure_is_actionable(self):
        self.env['CURL_STATUS'] = '22'
        result = self.launch('check')
        self.assertEqual(result.returncode, 1)
        self.assertIn('Service is not ready', result.stderr)
        self.assertIn('./launch logs', result.stderr)
        self.assertNotIn('Ready:', result.stdout)

    def test_custom_port_is_used(self):
        self.env['HOST_PORT'] = '08992'
        result = self.launch('check')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('http://127.0.0.1:8992/readyz', self.calls.read_text())

    def test_invalid_port_never_probes(self):
        for port in ['0', '65536', 'abc']:
            with self.subTest(port=port):
                self.env['HOST_PORT'] = port
                result = self.launch('check')
                self.assertEqual(result.returncode, 1)
                self.assertFalse(self.calls.exists())

    def test_start_waits_for_readiness(self):
        result = self.launch('start')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls.read_text()
        self.assertIn('docker compose up --build --detach', calls)
        self.assertIn('/readyz', calls)
        self.assertNotIn('/healthz', calls)

    def test_help_and_invalid_actions_do_not_require_docker(self):
        for args, status in [('help', 0), ('unknown', 2)]:
            with self.subTest(action=args):
                result = self.launch(args)
                self.assertEqual(result.returncode, status)
                self.assertFalse(self.calls.exists())
        help_text = self.launch('help').stdout
        self.assertIn('check', help_text)
        self.assertIn('checking does not require Docker', help_text)


if __name__ == '__main__':
    unittest.main()
