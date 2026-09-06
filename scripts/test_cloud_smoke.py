import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class CloudSmokeConfiguration(unittest.TestCase):
    def test_offline_check_and_private_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            path.write_text("HCLOUD_TOKEN=synthetic-private-test-token\n")
            os.chmod(path, 0o600)
            command = [sys.executable, str(Path(__file__).with_name("cloud_smoke.py")), "--check", "--env", str(path)]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("No API calls made", result.stdout)
            self.assertNotIn("synthetic-private-test-token", result.stdout + result.stderr)
            os.chmod(path, 0o644)
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("synthetic-private-test-token", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
