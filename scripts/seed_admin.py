#!/usr/bin/env python3
"""Opt-in local development bootstrap through the normal setup API; never reset users."""
import base64
import hashlib
import hmac
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import struct
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
ORIGIN = "http://localhost:8760"


def totp(secret, now=None):
    counter = int(time.time() if now is None else now) // 30
    key = base64.b32decode(secret + "=" * (-len(secret) % 8))
    digest = hmac.new(key, struct.pack(">Q", counter), hashlib.sha1).digest()
    offset = digest[-1] & 15
    return f"{(struct.unpack('>I', digest[offset:offset + 4])[0] & 0x7fffffff) % 1000000:06d}"


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError("Local seed endpoint redirected; no credentials forwarded.")


def main():
    settings = {}
    for line in (ROOT / ".env").read_text().splitlines():
        key, separator, value = line.partition("=")
        if separator and key in {"DEV_SEED_ADMIN", "DEV_SEED_ADMIN_EMAIL", "BOOTSTRAP_TOKEN"}:
            settings[key] = value.strip().strip("\"'")
    if os.environ.get("DEV_SEED_ADMIN", settings.get("DEV_SEED_ADMIN")) != "true":
        print("Optional development admin seed is disabled.")
        return
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def rpc(name, body):
        request = urllib.request.Request(f"{ORIGIN}/api/providah.v1.ConsoleService/{name}", data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "Origin": ORIGIN})
        try:
            with opener.open(request, timeout=15) as response:
                data = response.read(65537)
            if len(data) > 65536:
                raise RuntimeError("Unexpected oversized setup response.")
            return json.loads(data)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"Local admin setup failed at {name} (HTTP {error.code}); no existing user was reset.") from None
        except urllib.error.URLError:
            raise RuntimeError("Local console is unavailable; start it before seeding.") from None

    email = os.environ.get("DEV_SEED_ADMIN_EMAIL", settings.get("DEV_SEED_ADMIN_EMAIL", "admin@alphabravo.io"))
    def configure():
        subprocess.run(["docker", "compose", "exec", "-T", "-e", "DEV_SEED_ADMIN=true", "-e", "DEV_SEED_ADMIN_EMAIL=" + email, "app", "/providah", "seed-admin"], cwd=ROOT, check=True)
    if not rpc("SetupStatus", {}).get("required", False):
        configure()
        print("Installation initialized; optional admin seed checked without resetting passwords.")
        return
    token = settings.get("BOOTSTRAP_TOKEN", "")
    if len(token) < 32:
        raise RuntimeError("Missing local bootstrap configuration.")
    email = os.environ.get("DEV_SEED_ADMIN_EMAIL", settings.get("DEV_SEED_ADMIN_EMAIL", "admin@alphabravo.io"))
    enrollment = rpc("BeginSetup", {"token": token})
    credentials = {"url": ORIGIN, "email": email, "password": secrets.token_urlsafe(24), "totp_secret": enrollment["secret"], "authenticator_uri": enrollment["uri"], "organization": "Alpha Bravo", "role": "global administrator", "mfa_enabled": False}
    directory = ROOT / ".local"
    directory.mkdir(mode=0o700, exist_ok=True)
    if directory.is_symlink():
        raise RuntimeError("Refusing a symlink for local seed storage.")
    path = directory / "seed-admin.json"
    # Save before submission: a lost setup response must not lose the only credentials.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as output:
        os.fchmod(output.fileno(), 0o600)
        json.dump(credentials, output, indent=2)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())
    rpc("FinishSetup", {"token": token, "email": email, "password": credentials["password"], "code": totp(credentials["totp_secret"]), "organizationName": credentials["organization"]})
    session = rpc("GetSession", {})
    if session.get("email") != email or not any("members.manage" in org.get("permissions", []) for org in session.get("organizations", [])):
        raise RuntimeError("Setup returned unexpected administrator access; inspect the local installation.")
    rpc("Logout", {})
    configure()
    print(f"Development administrator created. Private login and authenticator details: {path}")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError) as error:
        raise SystemExit(str(error)) from None
