#!/usr/bin/env python3
"""Opt-in acceptance against an installed Mihomo binary, on isolated loopback.

Uses fake file providers, no listeners, no TUN, no health checks and no business
requests. Never reads or changes the running core's configuration. The output
contains counts and closed diagnostic fields, not controller bodies or secrets.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.request


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--core", required=True, type=Path)
    parser.add_argument("--topo", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    core, topo = args.core.resolve(), args.topo.resolve()
    report = {"schema": 1, "core_sha256": hashlib.sha256(core.read_bytes()).hexdigest(),
              "topo_version": subprocess.check_output([str(topo), "version"], text=True).strip(),
              "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "scope": "isolated core; fake file providers; controller reads only", "cases": []}
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def case(count: int, expected: str) -> dict:
        with tempfile.TemporaryDirectory(prefix="topophany-mihomo-accept-") as directory:
            root = Path(directory)
            token = secrets.token_hex(24)
            with socket.socket() as s:
                s.bind(("127.0.0.1", 0))
                port = s.getsockname()[1]
            def proxy(name):
                return {"name": name, "type": "ss", "server": "127.0.0.1", "port": 9,
                        "cipher": "aes-128-gcm", "password": "synthetic-unused-password"}
            (root / "large.json").write_text(json.dumps({"proxies": [proxy(f"node-{i:04d}") for i in range(count)]}))
            (root / "checked.json").write_text(json.dumps({"proxies": [proxy("known")]}))
            cfg = {"external-controller": f"127.0.0.1:{port}", "secret": token,
                   "port": 0, "socks-port": 0, "mixed-port": 0, "redir-port": 0, "tproxy-port": 0,
                   "mode": "rule", "log-level": "silent", "tun": {"enable": False},
                   "dns": {"enable": False}, "profile": {"store-selected": False},
                   "proxy-providers": {name: {"type": "file", "path": f"./{name}.json",
                       "health-check": {"enable": False}} for name in ("large", "checked")},
                   "proxy-groups": [{"name": "root", "type": "select", "use": ["large"]}],
                   "rules": ["MATCH,DIRECT"]}
            (root / "config.json").write_text(json.dumps(cfg))
            topology = {"schema": 1, "project": "mihomo-live-acceptance",
                "nodes": [{"id": "local", "name": "Isolated local core", "kind": "host"}],
                "observers": [{"id": "local", "name": "Local acceptance", "node": "local",
                               "context": "isolated core acceptance", "stale_seconds": 60}],
                "checks": [{"id": "core", "name": "Core inventory", "observer": "local", "kind": "mihomo",
                    "target": f"http://127.0.0.1:{port}", "interval_seconds": 60,
                    "timeout_seconds": 15, "stale_seconds": 180,
                    "adapter": {"token_env": "TOPO_ACCEPT_CORE_TOKEN", "root_proxy": "root",
                        "expected_mode": "rule", "expected_tun": False,
                        "expected_providers": {"checked": ["known"]}}}]}
            (root / "topology.json").write_text(json.dumps(topology))
            with (root / "core.log").open("wb") as log:
                proc = subprocess.Popen([str(core), "-d", str(root), "-f", str(root / "config.json")],
                                        stdout=log, stderr=subprocess.STDOUT)
                try:
                    def read(path):
                        req = urllib.request.Request(f"http://127.0.0.1:{port}" + path,
                                                     headers={"Authorization": "Bearer " + token})
                        with opener.open(req, timeout=3) as response:
                            body = response.read(8 * 1024 * 1024 + 1)
                        if len(body) > 8 * 1024 * 1024:
                            raise RuntimeError("acceptance response exceeds capture budget")
                        return json.loads(body), len(body)
                    deadline = time.monotonic() + 20
                    while True:
                        try:
                            version, _ = read("/version")
                            break
                        except Exception:
                            if proc.poll() is not None or time.monotonic() > deadline:
                                raise RuntimeError("isolated core did not become ready; private logs were not exported")
                            time.sleep(.1)
                    inventory, size = read("/proxies")
                    providers, provider_size = read("/providers/proxies")
                    names = set(inventory.get("proxies", {}))
                    for provider in providers.get("providers", {}).values():
                        names.update(p["name"] for p in provider.get("proxies", []) if p.get("name"))
                    env = dict(os.environ, TOPO_ACCEPT_CORE_TOKEN=token)
                    result = subprocess.run([str(topo), "once", "--config", str(root / "topology.json"),
                                             "--observer", "local"], capture_output=True, text=True,
                                            timeout=25, env=env)
                    if result.returncode != 0:
                        raise RuntimeError("topology or probe invocation failed; private output was not exported")
                    observation = json.loads(result.stdout)
                    if isinstance(observation, list):
                        observation = observation[0]
                    facts = observation.get("facts", {})
                    valid = (observation.get("outcome") == expected and observation.get("evidence") == "reported"
                             and not observation.get("connectivity"))
                    if expected == "ok":
                        valid = valid and "checked=match" in facts.get("provider_comparison", "")
                    else:
                        valid = valid and observation.get("code") == "proxy_inventory_capacity_exceeded"
                    encoded = result.stdout
                    withheld = token not in encoded and "synthetic-unused-password" not in encoded
                    valid = valid and withheld
                    return {"configured_nodes": count, "inventory_nodes": len(names),
                            "provider_members": len(providers["providers"]["large"]["proxies"]),
                            "inventory_bytes": size, "provider_bytes": provider_size,
                            "core_version": version, "outcome": observation.get("outcome"),
                            "code": observation.get("code"), "duration_ms": observation.get("duration_ms"),
                            "secrets_withheld": withheld, "pass": bool(valid)}
                finally:
                    proc.terminate()
                    try:
                        proc.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait(timeout=5)
    try:
        baseline = case(1, "ok")
        report["cases"].append(baseline)
        overhead = baseline["inventory_nodes"] - 1
        for count, expected in ((1000, "ok"), (4096 - overhead, "ok"), (4097 - overhead, "error")):
            item = case(count, expected)
            report["cases"].append(item)
            print(json.dumps(item), flush=True)
        report["pass"] = all(item["pass"] for item in report["cases"])
    except Exception as error:
        report.update({"pass": False, "error": type(error).__name__ + ": " + str(error)})
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    return 0 if report["pass"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
