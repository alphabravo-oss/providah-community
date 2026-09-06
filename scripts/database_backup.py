#!/usr/bin/env python3
"""Encrypted PostgreSQL logical backups using installed PostgreSQL and age tools."""
import argparse
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def run(args, **kwargs):
    result = subprocess.run(args, stderr=subprocess.DEVNULL, timeout=600, **kwargs)
    if result.returncode:
        raise RuntimeError(f"{args[0]} failed; check tool versions, credentials, permissions and input")


def pipeline(commands, output):
    processes = []
    try:
        for command in commands:
            previous = processes[-1].stdout if processes else None
            process = subprocess.Popen(command, stdin=previous, stdout=output if command is commands[-1] else subprocess.PIPE, stderr=subprocess.DEVNULL)
            processes.append(process)
            if previous:
                previous.close()
        codes = [process.wait(timeout=600) for process in reversed(processes)]
        if any(codes):
            raise RuntimeError("Backup pipeline failed; no archive was published")
    finally:
        for process in processes:
            if process.poll() is None:
                process.kill()
            process.wait()


def backup(archive, recipient):
    archive = Path(archive).absolute()
    if archive.exists() or archive.is_symlink():
        raise RuntimeError("Archive destination already exists")
    with tempfile.TemporaryDirectory(prefix=".providah-backup-", dir=archive.parent) as directory:
        staged = Path(directory) / "archive.age"
        with staged.open("xb") as output:
            os.chmod(staged, 0o600)
            pipeline([["pg_dump", "--format=custom", "--no-owner", "--no-acl"], ["age", "--encrypt", "--recipient", recipient]], output)
            output.flush()
            os.fsync(output.fileno())
        # Atomic publication without replacing any existing path, including symlinks.
        os.link(staged, archive)
        descriptor = os.open(archive.parent, os.O_RDONLY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)


def decrypt(archive, identity, directory):
    path = Path(directory) / "database.dump"
    with path.open("xb") as output:
        os.chmod(path, 0o600)
        run(["age", "--decrypt", "--identity", identity, str(archive)], stdout=output)
    # Authenticate the entire encrypted file before creating a database or restoring SQL.
    run(["pg_restore", "--list", str(path)], stdout=subprocess.DEVNULL)
    return path


def restore(archive, identity, target):
    if not re.fullmatch(r"providah_restore_[a-z0-9_]{1,40}", target):
        raise RuntimeError("Target must be a new database named providah_restore_<name>")
    maintenance = os.environ["PGDATABASE"]
    created = False
    with tempfile.TemporaryDirectory(prefix="providah-restore-") as directory:
        path = decrypt(archive, identity, directory)
        try:
            run(["createdb", "--maintenance-db", maintenance, "--", target], stdout=subprocess.DEVNULL)
            created = True
            run(["pg_restore", "--dbname", target, "--no-owner", "--no-acl", "--exit-on-error", "--single-transaction", str(path)], stdout=subprocess.DEVNULL)
            run(["psql", "--no-psqlrc", "--dbname", maintenance, "--set", "ON_ERROR_STOP=1", "--command", f'ALTER DATABASE "{target}" SET default_transaction_read_only = on'], stdout=subprocess.DEVNULL)
        except BaseException:
            if created:
                # Only this invocation's newly created target may be removed.
                result = subprocess.run(["dropdb", "--maintenance-db", maintenance, "--force", "--", target], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                if result.returncode:
                    print("Failed restore target remains; keep it disconnected and inspect it manually.", file=sys.stderr)
            raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("backup")
    create.add_argument("archive")
    create.add_argument("--recipient", required=True, help="age public recipient")
    verify = commands.add_parser("verify")
    verify.add_argument("archive")
    verify.add_argument("--identity", required=True, help="path to the offline age identity file")
    recover = commands.add_parser("restore")
    recover.add_argument("archive")
    recover.add_argument("target")
    recover.add_argument("--identity", required=True)
    args = parser.parse_args()
    # Connection passwords stay in libpq environment/passfiles, never command arguments.
    if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_-]{0,62}", os.environ.get("PGDATABASE", "")):
        parser.error("Set PGDATABASE to the source/maintenance database name (not a connection URL)")
    os.umask(0o077)
    try:
        if args.command == "backup":
            backup(args.archive, args.recipient)
            print("Encrypted backup created.")
        elif args.command == "verify":
            with tempfile.TemporaryDirectory(prefix="providah-verify-") as directory:
                decrypt(args.archive, args.identity, directory)
            print("Encryption and PostgreSQL archive directory verified; perform a restore drill to verify data.")
        else:
            restore(args.archive, args.identity, args.target)
            print("Restored into a new database with read-only defaults. Keep the app disconnected until recovery review.")
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(str(error) if isinstance(error, RuntimeError) else "Database backup operation failed; check tools and filesystem access.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
