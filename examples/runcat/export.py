#!/usr/bin/env python3
"""Publish one authenticated Topophany snapshot in RunCat Neo's JSON format."""
import argparse
import datetime as dt
import fcntl
import json
import math
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unicodedata

STOP = threading.Event()
UTC = dt.timezone.utc
ROW_WIDTH = 32


def width(text):
    return sum(0 if unicodedata.combining(c) else
               2 if unicodedata.east_asian_width(c) in ('W', 'F') else 1
               for c in text)


def compact(text, limit):
    text = ' '.join(str(text).split())
    if width(text) <= limit:
        return text
    result = ''
    for char in text:
        if width(result + char) > limit - 1:
            break
        result += char
    return result.rstrip() + '…'


def stamp(value):
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


def metric(title, value, percent=None):
    # RunCat uses fixedSize(), so even one long row widens the whole dashboard.
    title = compact(title, 14)
    row = {'title': title, 'formattedValue': compact(value, ROW_WIDTH - width(title) - 2)}
    if percent is not None and math.isfinite(percent) and 0 <= percent <= 100:
        row['normalizedValue'] = round(percent / 100, 5)
    return row


def state(rows):
    values = [r.get('health', 'unknown') for r in rows]
    for health in ('down', 'degraded', 'unknown'):
        if health in values:
            return health
    return 'up' if values else 'unknown'


def panel(snapshot, now=None, project=None):
    now = now or dt.datetime.now(UTC)
    generated = stamp(snapshot['generated_at'])
    if project is not None and snapshot.get('project') != project:
        raise ValueError('unexpected project')
    if not -5 <= (now - generated).total_seconds() < 15:
        raise ValueError('collector snapshot expired')
    topology = snapshot['topology']
    definitions = {c['id']: c for c in topology.get('checks', [])}
    checks = {c['id']: dict(c) for c in snapshot['checks']}
    for key, check in checks.items():
        last = check.get('last') or {}
        ttl = definitions.get(key, {}).get('stale_seconds', 0)
        sampled = stamp(last['sampled_at']) if last.get('sampled_at') else None
        if (check.get('freshness') != 'fresh' or sampled is None
                or not -5 <= (now - sampled).total_seconds() < ttl):
            check['health'] = 'unknown'
            check['freshness'] = check.get('freshness') if check.get('freshness') != 'fresh' else 'stale'

    counts = {h: sum(c['health'] == h for c in checks.values())
              for h in ('up', 'degraded', 'down', 'unknown')}
    incidents = snapshot.get('incidents') or []
    active_unknown = sum(c['health'] == 'unknown' and c.get('freshness') not in ('paused', 'maintenance')
                         for c in checks.values())
    observer_defs = {o['id']: o for o in topology.get('observers', [])}
    observers = snapshot.get('observers') or []
    live = sum(o.get('health') == 'up' and bool(o.get('last_seen'))
               and -5 <= (now - stamp(o['last_seen'])).total_seconds()
               < observer_defs.get(o['id'], {}).get('stale_seconds', 0)
               for o in observers)
    if incidents:
        headline = f'▲ {len(incidents)} open incident' + ('s' if len(incidents) != 1 else '')
        bar = f'▲ {len(incidents)}'
    elif counts['down']:
        headline, bar = f"✕ {counts['down']} checks down", f"✕ {counts['down']}"
    elif counts['degraded']:
        headline, bar = f"▲ {counts['degraded']} checks warn", f"▲ {counts['degraded']}"
    elif active_unknown or live < len(observers):
        headline, bar = '○ Incomplete evidence', '○ Check'
    elif not checks:
        headline, bar = '○ No checks configured', '○ —'
    elif counts['up'] == 0:
        headline, bar = '○ No current evidence', '○ —'
    else:
        headline, bar = '● All clear', f'● {live}/{len(observers)}'

    services = {s['id']: s for s in snapshot.get('services', [])}
    app_defs = topology.get('applications', [])
    app_health = {}
    for app in app_defs:
        members = [services.get(s['id'], {'health': 'unknown'})
                   for s in topology.get('services', []) if s.get('application') == app['id']]
        app_health[app['id']] = state(members)
    healthy = sum(h == 'up' for h in app_health.values())
    # Five metrics only. Coverage bars show known healthy members over the
    # whole fleet; unknown members must never make either bar look complete.
    paused = sum(c.get('freshness') == 'paused' for c in checks.values())
    maintenance = sum(c.get('freshness') == 'maintenance' for c in checks.values())
    coverage = str(active_unknown)
    if paused:
        coverage += f' · {paused} paused'
    if maintenance:
        coverage += f' · {maintenance} maint'
    rows = [
        metric('Status', headline),
        metric('Hosts', f'{live} / {len(observers)} online',
               100 * live / len(observers) if observers else None),
        metric('Apps', f'{healthy} / {len(app_defs)} healthy',
               100 * healthy / len(app_defs) if app_defs else None),
        metric('Checks', f"{counts['up']} up · {counts['degraded']} warn · {counts['down']} down"),
        metric('Unknown', coverage),
    ]
    return {'title': 'Topophany', 'symbol': 'network',
            'metricsBarValue': bar, 'metrics': rows,
            'lastUpdatedDate': generated.astimezone(UTC).strftime('%Y-%m-%dT%H:%M:%SZ')}


def unavailable():
    return {'title': 'Topophany', 'symbol': 'network', 'metricsBarValue': '○ Offline',
            'metrics': [metric('Network', '○ Collector unavailable'),
                        metric('Retry', 'Every 10s')],
            'lastUpdatedDate': dt.datetime.now(UTC).strftime('%Y-%m-%dT%H:%M:%SZ')}


def write(value, output):
    fd, temp = tempfile.mkstemp(prefix='.topophany-runcat-', suffix='.json', dir=output.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as handle:
            json.dump(value, handle, ensure_ascii=False, indent=2, allow_nan=False)
            handle.write('\n')
        os.replace(temp, output)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def refresh(args):
    command = [args.topo, '--json']
    if args.credentials:
        command.extend(['--credentials', args.credentials])
    if args.profile:
        command.extend(['--profile', args.profile])
    result = subprocess.run(command,
                            capture_output=True, text=True, timeout=12)
    if result.returncode:
        raise RuntimeError('authenticated collector read failed')
    write(panel(json.loads(result.stdout), project=args.project), args.output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--topo', default='topo', help='topo executable (default: PATH)')
    parser.add_argument('--credentials', help='saved client credentials; defaults to topo discovery')
    parser.add_argument('--profile', help='saved client profile; defaults to the active profile')
    parser.add_argument('--project', help='reject snapshots from a different project')
    parser.add_argument('--output', type=Path, required=True, help='RunCat custom metrics JSON path')
    parser.add_argument('--once', action='store_true')
    args = parser.parse_args()
    args.output = args.output.expanduser().absolute()
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with args.output.with_suffix(args.output.suffix + '.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        if args.once:
            refresh(args)
            return
        for sig in (signal.SIGTERM, signal.SIGINT):
            signal.signal(sig, lambda *_: STOP.set())
        failed = False
        while not STOP.is_set():
            started = time.monotonic()
            try:
                refresh(args)
                if failed:
                    print('Collector connection recovered', flush=True)
                failed = False
            except Exception as error:
                write(unavailable(), args.output)
                if not failed:
                    print('Overview unavailable:', type(error).__name__, file=sys.stderr, flush=True)
                failed = True
            STOP.wait(max(1, 10 - (time.monotonic() - started)))
        write(unavailable(), args.output)


if __name__ == '__main__':
    main()
