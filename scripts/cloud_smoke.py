#!/usr/bin/env python3
"""Read-only cloud discovery through the local console, broker and isolated workers."""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request

from seed_admin import NoRedirect, totp

ROOT = Path(__file__).resolve().parents[1]


def settings(path):
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise RuntimeError("Credential file must be a regular private file (chmod 600).")
    result = {}
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or key.strip() in result:
            raise RuntimeError("Invalid or duplicate .env setting.")
        result[key.strip()] = value.strip().strip("\"'")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env", type=Path, default=ROOT / ".env.cloud")
    parser.add_argument("--url", help="local console origin")
    parser.add_argument("--login-file", type=Path, help="local console login file")
    parser.add_argument("--output", type=Path, default=ROOT / ".local/cloud-smoke.json")
    parser.add_argument("--check", action="store_true", help="validate configuration without contacting any service")
    args = parser.parse_args()
    cfg = settings(args.env)
    origin = (args.url or cfg.get("PROVIDAH_URL", "http://localhost:8760")).rstrip("/")
    url = urllib.parse.urlsplit(origin)
    if url.scheme != "http" or url.hostname not in {"localhost", "127.0.0.1", "::1"} or url.username or url.password or url.path or url.query or url.fragment:
        raise RuntimeError("Smoke testing requires a local HTTP console origin.")
    providers = [
        ("hetzner", "", cfg.get("HCLOUD_TOKEN", "")),
        ("digitalocean", "", cfg.get("DIGITALOCEAN_TOKEN", "")),
    ]
    access, secret = cfg.get("AWS_ACCESS_KEY_ID", ""), cfg.get("AWS_SECRET_ACCESS_KEY", "")
    if bool(access) != bool(secret):
        raise RuntimeError("AWS requires both access key ID and secret access key.")
    if access:
        region = cfg.get("AWS_REGION", "")
        if not region:
            raise RuntimeError("Set an explicit AWS_REGION.")
        providers.append(("aws", region, json.dumps({"access_key_id": access, "secret_access_key": secret, "session_token": cfg.get("AWS_SESSION_TOKEN", ""), "account_id": cfg.get("AWS_EXPECTED_ACCOUNT_ID", "")})))
    providers = [p for p in providers if p[2]]
    if not providers:
        raise RuntimeError("No cloud credentials configured; fill in .env.cloud first.")
    if args.check:
        print("Configuration valid for: " + ", ".join(p[0] for p in providers) + ". No API calls made.")
        return
    login_path = args.login_file or Path(cfg.get("PROVIDAH_LOGIN_FILE", str(ROOT / ".local/seed-admin.json")))
    if login_path.is_symlink() or login_path.stat().st_mode & 0o077:
        raise RuntimeError("Login file must have private permissions.")
    login = json.loads(login_path.read_text())
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def rpc(name, body):
        req = urllib.request.Request(origin + "/api/providah.v1.ConsoleService/" + name, data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "Origin": origin})
        try:
            with opener.open(req, timeout=35) as response:
                raw = response.read(2**20 + 1)
            if len(raw) > 2**20:
                raise RuntimeError("Oversized console response.")
            return json.loads(raw)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"{name} failed (HTTP {error.code}); inspect the local console.") from None
        except urllib.error.URLError:
            raise RuntimeError("Local console unavailable.") from None

    rpc("Login", {"email": login["email"], "password": login["password"], "code": totp(login["totp_secret"]) if login.get("mfa_enabled", True) else ""})
    report = []
    try:
        session = rpc("GetSession", {})
        orgs = session.get("organizations", [])
        org = cfg.get("PROVIDAH_ORGANIZATION_ID", "")
        if not org and len(orgs) == 1:
            org = orgs[0]["id"]
        if not org or not any(o["id"] == org and "connections.manage" in o.get("permissions", []) for o in orgs):
            raise RuntimeError("Choose PROVIDAH_ORGANIZATION_ID with connection management permission.")
        for cloud, region, credential in providers:
            name = "cloud-smoke-" + cloud + "-" + secrets.token_hex(4)
            connection = rpc("CreateConnection", {"organizationId": org, "name": name, "provider": cloud, "region": region, "credential": credential})["connection"]
            scope = {"organizationId": org, "id": connection["id"]}
            try:
                # New connections are discovered automatically; polling avoids a second refresh race.
                deadline = time.monotonic() + 240
                while time.monotonic() < deadline:
                    connection = rpc("GetConnection", scope)["connection"]
                    state = connection.get("scanStatus", "")
                    if state in {"SCAN_STATUS_SUCCEEDED", "SCAN_STATUS_FAILED"}:
                        break
                    time.sleep(2)
                else:
                    raise RuntimeError(cloud + " discovery timed out.")
                if state != "SCAN_STATUS_SUCCEEDED":
                    raise RuntimeError(cloud + " discovery failed; inspect the connection in the console.")
                count, token = 0, ""
                while True:
                    page = rpc("ListResources", {"organizationId": org, "connectionId": connection["id"], "pageSize": 100, "pageToken": token})
                    count += len(page.get("resources", []))
                    token = page.get("nextPageToken", "")
                    if not token:
                        break
                    if count >= 100000:
                        raise RuntimeError("Inventory exceeded smoke-test bound.")
                report.append({"provider": cloud, "connection_id": connection["id"], "resources": count, "result": "passed"})
                print(f"{cloud}: discovery passed, {count} resources. No cloud mutations requested.")
            finally:
                rpc("SetConnectionEnabled", dict(scope, enabled=False))
    finally:
        rpc("Logout", {})
    output = args.output
    output.parent.mkdir(mode=0o700, exist_ok=True)
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as handle:
        os.fchmod(handle.fileno(), 0o600)
        json.dump(report, handle, indent=2)
    print("Smoke connections are disabled; revoke temporary cloud keys when finished.")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, ValueError, KeyError):
        # Never echo provider responses, credential values or unexpected exception contents.
        raise SystemExit("Cloud smoke test failed. Check configuration, local connection status and service health.") from None
