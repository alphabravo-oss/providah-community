#!/usr/bin/env python3
"""Create persistent local-only RustFS credentials without printing them."""
from pathlib import Path
import os
import json
import secrets

folder = Path(__file__).resolve().parent.parent / ".local"
folder.mkdir(mode=0o700, exist_ok=True)
target = folder / "dev-stack.env"
try:
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    if target.is_symlink() or not target.is_file():
        raise SystemExit("Refusing an unexpected dev-stack credential path")
else:
    with os.fdopen(fd, "w") as stream:
        stream.write(f"RUSTFS_ACCESS_KEY=providah-{secrets.token_hex(8)}\n")
        stream.write(f"RUSTFS_SECRET_KEY={secrets.token_urlsafe(36)}\n")
values = dict(line.split("=", 1) for line in target.read_text().splitlines() if "=" in line)
config = folder / "artifact-store.json"
if config.is_symlink():
    raise SystemExit("Refusing an unexpected artifact configuration path")
fd = os.open(config, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as stream:
    json.dump({"Endpoint": "http://rustfs:9000", "Region": "us-east-1", "Bucket": "providah-artifacts", "AccessKey": values["RUSTFS_ACCESS_KEY"], "SecretKey": values["RUSTFS_SECRET_KEY"]}, stream)
# Compose file-backed secrets retain host ownership on Linux. An env file lets
# the non-root application use private host credentials without relaxing file modes.
environment = folder / "artifact-store.env"
fd = os.open(environment, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
with os.fdopen(fd, "w") as stream:
    stream.write("ARTIFACT_STORE='" + config.read_text() + "'\n")
print("Local development credentials and artifact configuration ready in .local")
