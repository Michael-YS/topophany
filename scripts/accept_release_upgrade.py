#!/usr/bin/env python3
"""Opt-in native client upgrade/rollback acceptance in a private temporary install.

Uses a supplied test CA and an unused loopback address; starts no service,
performs no login/network request, and never uses the user's default profile.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import secrets
import subprocess
import tempfile
import time


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--previous', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--ca', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    previous, candidate, ca = args.previous.resolve(), args.candidate.resolve(), args.ca.resolve()
    def digest(p): return hashlib.sha256(p.read_bytes()).hexdigest()
    report = {'schema': 1, 'native_platform': platform.system(), 'native_machine': platform.machine(),
              'started_at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
              'scope': 'isolated native client install; no service, login or network request',
              'previous_sha256': digest(previous), 'candidate_sha256': digest(candidate), 'checks': [], 'pass': False}
    token = secrets.token_urlsafe(32)
    client_env = os.environ.copy()
    def run(exe, *words, secret_input=None):
        p = subprocess.run([str(exe), *map(str, words)], input=secret_input, text=True,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60, env=client_env)
        if p.returncode:
            raise RuntimeError('command failed: ' + (words[0] if words else 'version') + ' (private output retained only in memory)')
        if token in p.stdout or token in p.stderr:
            raise RuntimeError('test credential appeared in command output')
        return p.stdout.strip()
    try:
        before_version, candidate_version = run(previous, 'version'), run(candidate, 'version')
        report.update(previous_version=before_version, candidate_version=candidate_version)
        with tempfile.TemporaryDirectory(prefix='topophany-release-upgrade-') as directory:
            root = Path(directory).resolve()
            # os.UserConfigDir uses HOME on macOS, AppData on Windows and
            # XDG_CONFIG_HOME on Linux. Isolate all three before saving defaults.
            client_env.update(HOME=str(root / 'home'), APPDATA=str(root / 'config'),
                              XDG_CONFIG_HOME=str(root / 'config'))
            target = root / 'install'
            installed = target / 'bin' / ('topo.exe' if os.name == 'nt' else 'topo')
            def bundle(exe, name):
                dst = root / name
                run(exe, 'init', '--role', 'client', '--dir', dst, '--server', 'https://127.0.0.1:1',
                    '--project', 'release-acceptance', '--ca-file', ca, '--install-dir', target,
                    '--binary', exe, '--token-stdin', secret_input=token + '\n')
                return dst
            old_bundle = bundle(previous, 'previous-bundle')
            run(previous, 'install', '--bundle', old_bundle)
            assert digest(installed) == report['previous_sha256']
            assert run(installed, 'version') == before_version
            run(installed, 'preferences', 'set', '--page', 'servers', '--groups-collapsed=true',
                '--uniform-cards=true', '--full-map=true', '--map-mode', 'network', '--nerd-font-icons=true')
            preferences = json.loads(run(installed, 'preferences', 'show'))
            preference_files = list(root.rglob('preferences.json'))
            assert len(preference_files) == 1
            preference_path = preference_files[0]
            preference_bytes = preference_path.read_bytes()
            def check_preferences():
                assert json.loads(run(installed, 'preferences', 'show')) == preferences
                assert preference_path.read_bytes() == preference_bytes
            credentials = json.loads((target / 'credentials.json').read_text())
            backups = target / '.backups'
            existing = {p.name for p in backups.iterdir() if p.is_dir()} if backups.exists() else set()
            new_bundle = bundle(candidate, 'candidate-bundle')
            receipt = json.loads(run(candidate, 'install', '--bundle', new_bundle, '--dry-run'))['bundle_sha256']
            check_preferences()
            run(candidate, 'install', '--bundle', new_bundle, '--bundle-sha256', receipt)
            assert digest(installed) == report['candidate_sha256']
            assert run(installed, 'version') == candidate_version
            assert json.loads((target / 'credentials.json').read_text()) == credentials
            check_preferences()
            report['checks'].append({'name': 'previous client installs and upgrades to candidate with credentials preserved', 'pass': True})
            report['checks'].append({'name': 'saved user preferences survive dry-run and binary upgrade byte-for-byte', 'pass': True})
            created = [p for p in backups.iterdir() if p.is_dir() and p.name not in existing]
            assert len(created) == 1
            run(candidate, 'install', '--rollback', created[0])
            assert digest(installed) == report['previous_sha256']
            assert run(installed, 'version') == before_version
            assert json.loads((target / 'credentials.json').read_text()) == credentials
            check_preferences()
            report['checks'].append({'name': 'authenticated rollback restores exact previous executable and credentials', 'pass': True})
            run(candidate, 'install', '--bundle', new_bundle, '--bundle-sha256', receipt)
            assert digest(installed) == report['candidate_sha256']
            assert run(installed, 'version') == candidate_version
            check_preferences()
            report['checks'].append({'name': 'candidate installs again after rollback', 'pass': True})
            report['checks'].append({'name': 'saved user preferences survive rollback and reinstall byte-for-byte', 'pass': True})
        report['pass'] = True
    except Exception as error:
        report['error'] = type(error).__name__ + ': ' + str(error)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'platform': report['native_platform'], 'pass': report['pass'], 'checks': len(report['checks']), 'error': report.get('error')}))
    return 0 if report['pass'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
