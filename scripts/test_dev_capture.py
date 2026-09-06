#!/usr/bin/env python3
"""Exercise real Providah deliveries against the local development capture services."""
import base64
import http.cookiejar
import json
from pathlib import Path
import re
import secrets
import time
import urllib.request

root = Path(__file__).resolve().parent.parent
credentials = json.loads((root / ".local/seed-admin.json").read_text())
origin = "http://localhost:8760"
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

def api(method, **body):
    request = urllib.request.Request(origin + "/api/providah.v1.ConsoleService/" + method, data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "Origin": origin})
    with client.open(request, timeout=15) as response:
        return json.load(response)

def get(url):
    with urllib.request.urlopen(url, timeout=10) as response:
        return json.load(response)

def wait_for(check):
    until = time.monotonic() + 35
    while time.monotonic() < until:
        result = check()
        if result:
            return result
        time.sleep(.3)
    raise RuntimeError("Local capture did not arrive before the deadline")

login = api("Login", email=credentials["email"], password=credentials["password"])
if login.get("mfaRequired"):
    raise SystemExit("The seeded account now requires MFA; use the browser verification flow.")
try:
    organizations = api("GetSession")["organizations"]
    org = next((o for o in organizations if o["name"] == "Development testing"), None)
    if org is None:
        org = api("CreateOrganization", name="Development testing", password=credentials["password"])
    org_id = org["id"]
    catalog = api("ListNotificationDestinations", organizationId=org_id)
    assert catalog.get("developmentCapture"), "Start make dev-stack before this check"
    for kind, name, endpoint in [("email", "Mailpit development", "operator@providah.local"), ("webhook", "Webhookie development", "http://webhookie:8080/hooks/generic/default")]:
        existing = next((d for d in catalog.get("destinations", []) if d["name"] == name), None)
        if existing:
            assert existing["kind"] == kind and existing["endpoint"] == endpoint, "Existing test destination changed; refusing to overwrite it"
        else:
            api("SaveNotificationDestination", organizationId=org_id, name=name, kind=kind, endpoint=endpoint, usePlatformSmtp=kind == "email", signingSecret=base64.b64encode(secrets.token_bytes(32)).decode() if kind == "webhook" else "")
            updated = api("ListNotificationDestinations", organizationId=org_id)
            existing = next(d for d in updated["destinations"] if d["name"] == name)
        destination = existing["id"]
        if not existing.get("enabled"):
            api("SetNotificationDestinationEnabled", organizationId=org_id, id=destination, enabled=True)
        if not existing.get("verified"):
            seen_mail = {m["ID"] for m in get("http://localhost:18025/api/v1/messages")["messages"]}
            seen_hooks = {m["id"] for m in get("http://localhost:18080/api/v1/events")["data"]}
            api("SendNotificationTest", organizationId=org_id, id=destination, verification=True)
            def codes():
                if kind == "email":
                    found = {}
                    for message in get("http://localhost:18025/api/v1/messages")["messages"]:
                        if message["ID"] in seen_mail:
                            continue
                        text = get("http://localhost:18025/api/v1/message/" + message["ID"]).get("Text", "")
                        for label, key in [("Destination code", "code"), ("Sender code", "senderCode")]:
                            match = re.search(label + r": ([a-f0-9]{64})", text)
                            if match:
                                found[key] = match.group(1)
                    return found if len(found) == 2 else None
                for event in get("http://localhost:18080/api/v1/events")["data"]:
                    if event["id"] in seen_hooks:
                        continue
                    body = event.get("body", {})
                    if isinstance(body, str):
                        try:
                            body = json.loads(body)
                        except ValueError:
                            continue
                    if isinstance(body, dict) and body.get("verification_code"):
                        return {"code": body["verification_code"]}
            api("VerifyNotificationDestination", organizationId=org_id, id=destination, **wait_for(codes))
        before = {d["id"] for d in api("ListNotificationDeliveries", organizationId=org_id).get("deliveries", [])}
        api("SendNotificationTest", organizationId=org_id, id=destination)
        def delivered():
            return next((d for d in api("ListNotificationDeliveries", organizationId=org_id).get("deliveries", []) if d["id"] not in before and d.get("destinationName") == name and d.get("status") == "delivered"), None)
        wait_for(delivered)
        print(name + ": verification and real queued delivery passed")
finally:
    api("Logout")
