#!/usr/bin/env python3
"""Build and admit local offline validators, preserving unrelated runtime entries."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
CATALOG = ROOT / '.local/automation-runtimes.json'
ENV = ROOT / '.env'


def docker(*args):
    return subprocess.check_output(['docker', *args], cwd=ROOT, text=True).strip()


def replace_catalog(lines, owned, replacements):
    entries = [line for line in lines if line.startswith('AUTOMATION_RUNTIMES=')]
    if len(entries) > 1:
        raise ValueError('Duplicate AUTOMATION_RUNTIMES settings')
    value = entries[0].split('=', 1)[1].strip() if entries else ''
    if value[:1] in {'\"', "'"} and value[-1:] == value[:1]:
        value = value[1:-1]
    existing = json.loads(value) if value else []
    if not isinstance(existing, list):
        raise ValueError('AUTOMATION_RUNTIMES must be a JSON list')
    # Remove only complete entries this dev setup previously installed.
    updated = [entry for entry in existing if entry not in owned]
    for entry in replacements:
        if entry not in updated:
            if any(isinstance(current, dict) and (current.get("runtime"), current.get("image")) == (entry["runtime"], entry["image"]) for current in updated):
                raise ValueError("An existing runtime has a different policy for this image")
            updated.append(entry)
    if len(updated) > 32:
        raise ValueError('Runtime catalog exceeds 32 entries')
    encoded = json.dumps(updated, separators=(',', ':'))
    if "'" in encoded:
        raise ValueError("Runtime catalog contains an unsafe env-file quote")
    text = "AUTOMATION_RUNTIMES='" + encoded + "'"
    return [line for line in lines if not line.startswith('AUTOMATION_RUNTIMES=')] + [text]


def write_private(path, text):
    if path.is_symlink():
        raise ValueError('Refusing a symlink configuration path')
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            output.write(text)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    stop = sys.argv[1:] == ['--stop']
    if sys.argv[1:] and not stop:
        raise SystemExit('Usage: dev_automation.py [--stop]')
    if ENV.is_symlink() or CATALOG.is_symlink():
        raise SystemExit('Refusing symlink runtime configuration')
    lines = ENV.read_text().splitlines()
    owned = json.loads(CATALOG.read_text()) if CATALOG.exists() else []
    # Validate existing settings before building images.
    replace_catalog(lines, owned, [])
    replacements = []
    inventory = {}
    if not stop:
        for engine, cli, version in [('opentofu', 'tofu', '1.11.6'), ('terraform', 'terraform', '1.14.5'), ('ansible', 'ansible-playbook', '2.19.7')]:
            tag = f'providah-automation-{engine}:dev'
            subprocess.run(['docker', 'build', '-f', 'Dockerfile.automation-examples', '--target', engine, '-t', tag, '.bin/automation-image'], cwd=ROOT, check=True)
            image = docker('image', 'inspect', tag, '--format', '{{.Id}}')
            if not re.fullmatch(r'sha256:[a-f0-9]{64}', image):
                raise ValueError('Image did not resolve to an immutable ID')
            restrictions = ['run', '--rm', '--network=none', '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--tmpfs', '/tmp:rw,nosuid,nodev,size=64m', '-e', 'HOME=/tmp']
            output = docker(*restrictions, '--entrypoint', cli, image, *(['--version'] if engine == 'ansible' else ['version', '-json']))
            actual = re.search(r'\[core ([0-9.]+)\]', output).group(1) if engine == 'ansible' else json.loads(output)['terraform_version']
            if actual != version:
                raise ValueError(f'{engine} version differs from the pinned example')
            inventory[engine] = {'image': image, 'version': actual}
            if engine == 'ansible':
                inventory[engine]['packages'] = json.loads(docker(*restrictions, '--entrypoint', 'python', image, '-m', 'pip', 'list', '--format=json'))
            checks = dict(os.environ, TEST_AUTOMATION_IMAGE=image, TEST_AUTOMATION_RUNTIME=engine)
            checks.pop('TEST_DEPENDENCY_BUNDLE', None)
            subprocess.run(['go', 'test', './internal/launcher', '-run', '^TestAutomationContainer$', '-count=1'], cwd=ROOT, env=checks, check=True)
            replacements.append({'runtime': engine, 'image': image, 'version': actual})
    updated = replace_catalog(lines, owned, replacements)
    CATALOG.parent.mkdir(mode=0o700, exist_ok=True)
    if not stop:
        write_private(CATALOG.parent / 'automation-image-inventory.json', json.dumps(inventory, indent=2) + '\n')
    # The manifest tracks only entries we own; keep it until configuration is saved.
    write_private(ENV, '\n'.join(updated) + '\n')
    write_private(CATALOG, json.dumps(replacements, indent=2) + '\n')
    print('Local offline validators disabled.' if stop else 'Three pinned offline validators configured; image inventory saved in .local.')


if __name__ == '__main__':
    main()
