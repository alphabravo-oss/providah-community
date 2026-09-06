"""Opt-in real PostgreSQL/age round-trip check; all databases and keys are disposable."""
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("database_backup.py")


@unittest.skipUnless(os.environ.get("TEST_BACKUP") == "1", "run make test-backup with matching PostgreSQL tools")
class DatabaseBackupTest(unittest.TestCase):
    def test_round_trip_and_refusals(self):
        source = "providah_backup_test_" + secrets.token_hex(6)
        target = "providah_restore_test_" + secrets.token_hex(6)
        broken = "providah_restore_bad_" + secrets.token_hex(6)
        environment = dict(os.environ)
        maintenance = environment["PGDATABASE"]

        def command(args, **kwargs):
            return subprocess.run(args, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, **kwargs).stdout

        def sql(database, statement):
            return command(["psql", "--no-psqlrc", "--dbname", database, "-At", "--set", "ON_ERROR_STOP=1", "--command", statement]).strip()

        def tool(*args, okay=True):
            result = subprocess.run([sys.executable, str(SCRIPT), *map(str, args)], env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertEqual(result.returncode == 0, okay, result.stderr.decode())
            return result

        command(["createdb", "--maintenance-db", maintenance, source])
        try:
            sql(source, "CREATE TABLE recovery_probe(id integer PRIMARY KEY, body text NOT NULL); INSERT INTO recovery_probe SELECT n,repeat('private-backup-fixture-',2000) FROM generate_series(1,100)n")
            digest = sql(source, "SELECT count(*)||':'||md5(string_agg(body,'' ORDER BY id)) FROM recovery_probe")
            environment["PGDATABASE"] = source
            with tempfile.TemporaryDirectory() as directory:
                directory = Path(directory)
                identity, wrong = directory / "identity", directory / "wrong"
                command(["age-keygen", "-o", str(identity)])
                command(["age-keygen", "-o", str(wrong)])
                recipient = command(["age-keygen", "-y", str(identity)]).decode().strip()
                archive = directory / "database.age"
                tool("backup", archive, "--recipient", recipient)
                original = archive.read_bytes()
                self.assertEqual(archive.stat().st_mode & 0o777, 0o600)
                self.assertNotIn(b"private-backup-fixture", original)
                tool("backup", archive, "--recipient", recipient, okay=False)
                self.assertEqual(archive.read_bytes(), original)
                tool("verify", archive, "--identity", identity)
                tool("verify", archive, "--identity", wrong, okay=False)
                tool("restore", archive, target, "--identity", identity)
                self.assertEqual(sql(target, "SELECT count(*)||':'||md5(string_agg(body,'' ORDER BY id)) FROM recovery_probe"), digest)
                self.assertEqual(sql(target, "SHOW default_transaction_read_only"), b"on")
                denied = subprocess.run(["psql", "--dbname", target, "--set", "ON_ERROR_STOP=1", "--command", "DELETE FROM recovery_probe"], env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                self.assertNotEqual(denied.returncode, 0)
                tool("restore", archive, target, "--identity", identity, okay=False)
                self.assertEqual(sql(target, "SELECT count(*) FROM recovery_probe"), b"100")
                tool("restore", archive, source, "--identity", identity, okay=False)
                truncated = directory / "truncated.age"
                truncated.write_bytes(original[:-10])
                tool("restore", truncated, broken, "--identity", identity, okay=False)
                self.assertEqual(sql(maintenance, f"SELECT count(*) FROM pg_database WHERE datname='{broken}'"), b"0")
                absent = directory / "failed.age"
                tool("backup", absent, "--recipient", "invalid", okay=False)
                self.assertFalse(absent.exists())
        finally:
            for database in (source, target, broken):
                subprocess.run(["dropdb", "--maintenance-db", maintenance, "--if-exists", "--force", database], env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    unittest.main()
