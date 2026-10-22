#!/usr/bin/env python3
"""D1 acceptance against a real topo binary and a literal-loopback synthetic Hub.

Never discovers a real account or reads user credentials. All secrets are
synthetic and supplied via stdin or a 0600 ready file. Python stdlib only.
"""
from __future__ import annotations
import argparse
import base64
import datetime as dt
import hashlib
import http.server
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import tempfile
import threading
import time
import urllib.parse

IDENTITY = "fixture@example.invalid"
PASSWORD = "D1-synthetic-password-canary"
SUBJECT = "uuuuuuuuuuuuuuu"

def token():
    claims = {"id": SUBJECT, "collectionId": "users", "type": "auth", "exp": int(time.time()) + 3600, "refreshable": True}
    return "synthetic." + base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip("=") + ".D1-secret-canary"

def utc(seconds):
    return dt.datetime.fromtimestamp(seconds, dt.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")

class FixtureHub:
    def __init__(self, objects=20, scenario="ready", delay=0):
        self.lock = threading.Lock()
        self.objects, self.scenario, self.delay = objects, scenario, delay
        self.sample_at = time.time()
        self.sample_id = "sssssssssssssss"
        self.counts = {}
        self.token = token()
        owner = self
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_): pass
            def do_GET(self): self.handle_api()
            def do_POST(self): self.handle_api()
            def handle_api(self):
                parsed = urllib.parse.urlsplit(self.path)
                query = urllib.parse.parse_qs(parsed.query)
                path = parsed.path
                with owner.lock:
                    key = self.command + " " + path
                    owner.counts[key] = owner.counts.get(key, 0) + 1
                    scenario, delay, objects, sample_at, sample_id = owner.scenario, owner.delay, owner.objects, owner.sample_at, owner.sample_id
                if delay:
                    time.sleep(delay)
                status, body = 200, {}
                is_auth = path.endswith("auth-with-password") or path.endswith("auth-refresh")
                if is_auth:
                    if scenario == "auth_required" and path.endswith("auth-refresh"): status = 401
                    elif scenario == "mfa": body = {"mfaId": "fixture-mfa"}
                    else: body = {"token": owner.token, "record": {"id": SUBJECT, "collectionName": "users"}}
                elif path.startswith("/api/collections/users/records/"):
                    body = {"id": SUBJECT}
                elif self.headers.get("Authorization") != owner.token:
                    status = 401
                elif path == "/api/collections/systems/records":
                    page = int(query.get("page", [1])[0])
                    items = [{"id": f"{i:015d}", "name": f"Synthetic server {i + 1}"} for i in range((page-1)*100, min(page*100, objects))]
                    body = {"page": page, "perPage": 100, "totalItems": objects, "totalPages": (objects+99)//100, "items": items}
                elif path == "/api/collections/system_stats/records":
                    if scenario in ("auth_required", "forbidden", "rate_limited", "unavailable"):
                        status = {"auth_required": 401, "forbidden": 403, "rate_limited": 429, "unavailable": 503}[scenario]
                    else:
                        object_id = query.get("filter", ['system = "000000000000000"'])[0].split('"')[1]
                        at = sample_at - (600 if scenario == "stale" else 0)
                        items = [] if scenario == "missing" else [{"id": sample_id, "system": object_id, "type": "1m", "created": utc(at),
                            "stats": {"cpu": 25, "mem": [9007199254740993, 4503599627370496, 0, 0], "mp": 50, "d": 100, "du": 25, "dp": 25}}]
                        body = {"page": 1, "perPage": 1, "totalItems": -1, "totalPages": -1, "items": items}
                else: status = 404
                data = json.dumps(body, separators=(",", ":")).encode()
                try:
                    self.send_response(status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    if status == 429: self.send_header("Retry-After", "60")
                    self.end_headers()
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError): pass
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = f"http://127.0.0.1:{self.server.server_port}"
    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
    def count(self):
        with self.lock: return self.counts.get("GET /api/collections/system_stats/records", 0)

def source(hub, source_id="bs-00000000000000000000000000000001"):
    return {"id": source_id, "name": "Synthetic Beszel", "adapter": "beszel", "url": hub.url}

def serve(args):
    hub = FixtureHub(args.objects, args.scenario, args.delay_seconds)
    target = args.ready_file
    ready = {"schema": 1, "url": hub.url, "identity": IDENTITY, "password": PASSWORD, "token": hub.token,
             "systems": [f"{i:015d}" for i in range(args.objects)], "scenario": args.scenario}
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as f: json.dump(ready, f); f.write("\n")
    stopped = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM): signal.signal(sig, lambda *_: stopped.set())
    try:
        while not stopped.wait(0.5): pass
    finally: hub.close()
    return 0

class Stream:
    def __init__(self, binary, root):
        self.proc = subprocess.Popen([str(binary), "monitor", "stream", "--direct-root", str(root)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.frames = queue.Queue(maxsize=128)
        self.history = []
        def read():
            for line in self.proc.stdout:
                if len(line.encode()) > 1 << 20: self.frames.put(RuntimeError("oversized frame")); return
                self.frames.put(json.loads(line))
        self.reader = threading.Thread(target=read, daemon=True)
        self.reader.start()
    def send(self, req):
        self.proc.stdin.write(json.dumps(req, separators=(",", ":")) + "\n"); self.proc.stdin.flush()
    def until(self, predicate, timeout=8):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            frame = self.frames.get(timeout=max(0.01, end-time.monotonic()))
            if isinstance(frame, Exception): raise frame
            self.history.append(frame)
            if predicate(frame): return frame
        raise AssertionError("expected stream frame did not arrive")
    def close(self):
        if self.proc.poll() is None:
            self.proc.stdin.close()
            try: self.proc.wait(timeout=4)
            except subprocess.TimeoutExpired:
                self.proc.kill(); self.proc.wait(); raise AssertionError("stream did not exit at EOF")
        self.reader.join(timeout=2)
        diagnostic = self.proc.stderr.read()
        assert PASSWORD not in diagnostic and "D1-secret-canary" not in diagnostic
        assert self.proc.returncode == 0, f"stream exit {self.proc.returncode}"

def status(frame):
    for row in frame.get("rows", []):
        if row["metric"] == "sources.status": return row.get("value", {}).get("state")

def acceptance(args):
    args.out.mkdir(parents=True, exist_ok=True)
    hub = FixtureHub(20)
    other = FixtureHub(1, "forbidden")
    results = []
    artifacts = {}
    stream = None
    begin = time.monotonic()
    try:
        with tempfile.TemporaryDirectory(prefix="direct-", dir=args.out) as work:
            root = Path(work) / "private"
            def command(command, payload=None, expected=0):
                proc = subprocess.run([str(args.topo), *command, "--direct-root", str(root)], input=None if payload is None else json.dumps(payload), text=True, capture_output=True, timeout=35)
                assert proc.returncode == expected, f"{command[:2]} exit={proc.returncode} diagnostic={proc.stderr[:256]}"
                assert PASSWORD not in proc.stdout + proc.stderr and hub.token not in proc.stdout + proc.stderr
                assert len(proc.stdout.encode()) <= 1 << 20
                return json.loads(proc.stdout)
            caps = command(["monitor", "stream", "--capabilities"])
            assert caps["direct"]["config_api"] == 1 and "direct" in caps["sources"]
            assert not root.exists(), "capabilities created private directory"
            req = {"schema": 1, "request_id": "fixture-setup", "profile": "desktop", "source": source(hub), "auth": {"mode": "password", "identity": IDENTITY, "password": PASSWORD}}
            inspected = command(["sources", "inspect", "--json-stdin"], req)
            assert inspected["ok"] and len(inspected["data"]["systems"]) == 20 and not inspected["data"]["truncated"]
            assert not root.exists(), "inspect wrote config/credentials"
            req.update(base_revision="", selected_objects=[f"{i:015d}" for i in range(20)])
            saved = command(["sources", "save", "--json-stdin"], req)["data"]
            rev, dataset = saved["profile_revision"], saved["dataset_id"]
            assert len(saved["nodes"]) == 20 and saved["nodes"][0]["id"] == req["source"]["id"] + ".000000000000000"
            listed = command(["sources", "list", "--json"])
            assert listed["data"]["profiles"][0]["dataset_id"] == dataset
            conflict = command(["sources", "save", "--json-stdin"], req, 1)
            assert conflict["error"]["code"] == "profile_conflict"
            public = (root / "profiles.json").read_text()
            assert PASSWORD not in public and hub.token not in public and "credential_env" not in public
            results.append({"name": "config-inspect-save-list-cas", "status": "pass"})
            template = {"schema": 1, "title": "Synthetic D1", "bar": {"row": "status"}, "rows": [{"id": "status", "label": "", "metric": "sources.status", "visual": "status"}, {"id": "servers", "label": "", "metric": "servers.list", "visual": "list"}]}
            request = {"schema": 1, "request_id": "one", "source": "direct", "profile": "desktop", "profile_revision": rev, "interval": "1s", "template": template}
            collection_started=time.monotonic()
            stream = Stream(args.topo, root)
            stream.send(request)
            first = stream.until(lambda f: f.get("request_id") == "one")
            first_loading_seconds=time.monotonic()-collection_started
            assert first["lifecycle"] == "running" and status(first) == "loading"
            assert first_loading_seconds<2, "first loading frame waited for collection"
            ready = stream.until(lambda f: status(f) == "ready")
            first_ready_seconds=time.monotonic()-collection_started
            assert ready["profile"] == "desktop" and ready["dataset_id"] == dataset
            assert hub.count() == 20, f"20 objects used {hub.count()} requests"
            request["request_id"] = "two"; request["template"]["title"] = "Changed projection"
            stream.send(request); stream.until(lambda f: f.get("request_id") == "two")
            time.sleep(1.1)
            assert hub.count() == 20, "template/display tick recollected"
            request["profile_revision"] = "stale-version"; request["request_id"] = "conflict"
            stream.send(request)
            error = stream.until(lambda f: f.get("request_id") == "conflict")
            assert error["lifecycle"] == "error" and error["error"]["code"] == "profile_conflict"
            request["profile_revision"] = rev; request["request_id"] = "retry"; request["retry_nonce"] = "retry-one"
            stream.send(request); stream.until(lambda f: f.get("request_id") == "retry" and f["lifecycle"] == "running")
            deadline = time.monotonic() + 3
            while hub.count() < 40 and time.monotonic() < deadline: time.sleep(0.01)
            assert hub.count() == 40
            stream.send(request); time.sleep(0.2); assert hub.count() == 40, "duplicate nonce retried"
            results.append({"name": "stream-loading-ready-projection-cas-retry-20-object-budget", "status": "pass", "resource_requests": hub.count(), "first_loading_seconds":round(first_loading_seconds,3),"first_ready_seconds":round(first_ready_seconds,3)})
            req2 = {**req, "base_revision": rev, "source": source(other, "bs-00000000000000000000000000000002"), "selected_objects": ["000000000000000"]}
            saved2 = command(["sources", "save", "--json-stdin"], req2)["data"]
            request.update(request_id="two-sources", profile_revision=saved2["profile_revision"], retry_nonce="")
            stream.send(request)
            partial = stream.until(lambda f: f.get("request_id") == "two-sources" and status(f) == "partial")
            counts = partial["rows"][0]["value"]["counts"]
            assert counts["current"] == 20 and counts["missing"] == 1 and counts["selected"] == 21
            assert hub.count() == 40, "adding another source recollected unchanged source"
            partial_status=command(["status", "--source", "direct", "--profile", "desktop", "--json"],expected=2)
            assert partial_status["sources_status"]["state"]=="partial"
            results.append({"name": "multi-source-isolation-reconfigure", "status": "pass"})
            stream.close()
            artifacts["stream"] = stream.history
            stream = None
            removed = command(["sources", "remove", "--json-stdin"], {"schema": 1, "profile": "desktop", "base_revision": saved2["profile_revision"], "source_id": req2["source"]["id"]})
            assert removed["ok"]
            current = command(["--source", "direct", "--profile", "desktop", "--json"])
            assert "topology" in current and current["sources_status"]["state"] == "ready"
            assert current["external"][0]["report"]["resource"]["memory"]["total_bytes"] == 9007199254740993
            cli_status = command(["status", "--source", "direct", "--profile", "desktop", "--json"])
            assert "verdict" in cli_status and "topology" not in cli_status and cli_status["status"] == "clear"
            servers = command(["show", "servers", "--source", "direct", "--profile", "desktop", "--json"])
            assert len(servers["rows"]) == 20 and servers["rows"][0]["card"]["resources"][0]["origin"]["adapter"] == "beszel"
            with hub.lock: hub.scenario = "missing"
            no_data = command(["status", "--source", "direct", "--profile", "desktop", "--json"], expected=3)
            assert no_data["sources_status"]["state"] == "no_data"
            with hub.lock: hub.scenario = "unavailable"
            unavailable = command(["status", "--source", "direct", "--profile", "desktop", "--json"], expected=4)
            assert unavailable["sources_status"]["state"] == "unavailable"
            with hub.lock: hub.scenario = "ready"
            history = subprocess.run([str(args.topo), "history", "not-a-check", "--source", "direct", "--profile", "desktop", "--direct-root", str(root), "--json"], text=True, capture_output=True, timeout=3)
            assert history.returncode == 1 and "history_unsupported_in_direct_mode" in history.stderr
            panel_dir = Path(work) / "panels"
            panel_output = Path(work) / "metrics.json"
            command(["monitor", "configure", "desktop", "--source", "direct", "--profile", "desktop", "--monitor-dir", str(panel_dir), "--output", str(panel_output), "--interval", "1s"])
            for _ in range(2):
                proc = subprocess.Popen([str(args.topo), "monitor", "run", "desktop", "--monitor-dir", str(panel_dir), "--direct-root", str(root)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                try:
                    end = time.monotonic() + 4
                    while time.monotonic() < end:
                        panel = json.loads(panel_output.read_text())
                        if panel["lifecycle"] == "running" and status(panel) == "ready": break
                        time.sleep(0.02)
                    else: raise AssertionError("direct file monitor never ready")
                    proc.send_signal(signal.SIGTERM)
                    _, diagnostic = proc.communicate(timeout=4)
                    assert proc.returncode == 0 and PASSWORD not in diagnostic
                    assert json.loads(panel_output.read_text())["lifecycle"] == "stopped"
                finally:
                    if proc.poll() is None: proc.kill(); proc.wait()
            results.append({"name": "all-current-cli-views-exit-codes-history-file-monitor-restart", "status": "pass"})
            # A short data window and unchanged raw record exercise expiry while
            # successful contact keeps advancing. This uses an explicitly 1s source.
            with hub.lock: hub.sample_at = time.time()
            expiry_req = {**req, "profile": "expiry", "base_revision": "", "source": {**req["source"], "interval_seconds": 1, "max_age_seconds": 3}, "selected_objects": ["000000000000000"]}
            expiry = command(["sources", "save", "--json-stdin"], expiry_req)["data"]
            exp_request = {**request, "request_id": "expiry", "profile": "expiry", "profile_revision": expiry["profile_revision"], "retry_nonce": ""}
            stream = Stream(args.topo, root); stream.send(exp_request)
            stream.until(lambda f: status(f) == "ready")
            expired = stream.until(lambda f: status(f) == "unavailable", timeout=6)
            assert expired["rows"][0]["value"]["counts"]["stale"] == 1
            with hub.lock: hub.sample_at = time.time(); hub.sample_id = "ttttttttttttttt"; hub.scenario = "unavailable"
            stream.until(lambda f: any(s["state"] == "retrying" for s in f.get("rows", [{}])[0].get("value", {}).get("sources", [])), timeout=4)
            with hub.lock: hub.scenario = "ready"
            exp_request["retry_nonce"] = "recover-one"; exp_request["request_id"] = "recover"
            stream.send(exp_request); stream.until(lambda f: f.get("request_id") == "recover" and status(f) == "ready", timeout=5)
            stream.close(); stream = None
            results.append({"name": "same-record-expiry-network-recovery-without-process-restart", "status": "pass"})
            if os.name == "posix":
                import pty, fcntl, termios, struct
                master, slave = pty.openpty()
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
                env = dict(os.environ, TERM="xterm-256color")
                proc = subprocess.Popen([str(args.topo), "--source", "direct", "--profile", "desktop", "--direct-root", str(root), "--preferences", str(Path(work)/"preferences.json"), "--no-color", "--page", "servers"], stdin=slave, stdout=slave, stderr=subprocess.PIPE, env=env)
                os.close(slave)
                rendered = bytearray()
                def drain():
                    try:
                        while len(rendered) < 512*1024:
                            chunk = os.read(master, 8192)
                            if not chunk: break
                            rendered.extend(chunk)
                    except OSError: pass
                reader = threading.Thread(target=drain, daemon=True); reader.start()
                try:
                    end = time.monotonic()+4
                    while b"Beszel direct" not in rendered and time.monotonic() < end: time.sleep(0.02)
                    os.write(master, b"q")
                    _, diagnostic = proc.communicate(timeout=4)
                    assert proc.returncode == 0, diagnostic.decode(errors="replace")
                    text = rendered.decode(errors="replace")
                    assert "Beszel direct" in text and "Connecting to the collector" not in text and "ALL CLEAR" not in text
                    assert PASSWORD not in text and hub.token not in text
                finally:
                    if proc.poll() is None: proc.kill(); proc.wait()
                    os.close(master); reader.join(timeout=1)
                results.append({"name": "real-tui-pty-direct-identity-and-exit", "status": "pass"})
            # A blocked stdout must not stop source collection or EOF cleanup.
            slow_template = {"schema": 1, "title": "Bounded slow consumer", "bar": {"row": "status"}, "rows": [{"id": "status", "metric": "sources.status", "visual": "status"}] + [{"id": f"card-{i}", "metric": "server.card", "visual": "card", "select": {"node": req["source"]["id"] + ".000000000000000"}} for i in range(127)]}
            slow_request = {"schema": 1, "request_id": "slow", "source": "direct", "profile": "desktop", "template": slow_template, "interval": "1s"}
            before = hub.count()
            slow = subprocess.Popen([str(args.topo), "monitor", "stream", "--direct-root", str(root)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                slow.stdin.write(json.dumps(slow_request)+"\n"); slow.stdin.flush()
                end = time.monotonic()+3
                while hub.count() < before+20 and time.monotonic()<end: time.sleep(0.02)
                assert hub.count() == before+20, "slow stdout blocked acquisition"
                slow.stdin.close(); slow.wait(timeout=4)
                assert slow.returncode == 0
            finally:
                if slow.poll() is None: slow.kill(); slow.wait()
                slow.stdout.close(); slow.stderr.close()
            for sig in (signal.SIGINT, signal.SIGTERM):
                child = Stream(args.topo, root); child.send({**slow_request, "template": template}); child.until(lambda f: f["lifecycle"] == "running")
                child.proc.send_signal(sig)
                child.until(lambda f: f["lifecycle"] == "stopped", timeout=3); child.close()
            broken = subprocess.Popen([str(args.topo), "monitor", "stream", "--direct-root", str(root)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                broken.stdout.close(); broken.stdin.write(json.dumps(slow_request)+"\n"); broken.stdin.flush(); broken.wait(timeout=4)
                assert broken.returncode == 0
            finally:
                if broken.poll() is None: broken.kill(); broken.wait()
                broken.stdin.close(); broken.stderr.close()
            results.append({"name": "slow-stdout-eof-epipe-sigint-sigterm-bounded-shutdown", "status": "pass"})
            artifacts["capabilities"] = caps
            artifacts["profiles"] = listed
    finally:
        if stream is not None: stream.close()
        hub.close(); other.close()
        summary = {"schema": 1, "results": results, "elapsed_seconds": round(time.monotonic()-begin, 3)}
        (args.out / "results.json").write_text(json.dumps(summary, indent=2) + "\n")
        for name, value in artifacts.items():
            encoded = json.dumps(value, indent=2)
            assert PASSWORD not in encoded and "D1-secret-canary" not in encoded
            (args.out / (name + ".json")).write_text(encoded + "\n")
    print(json.dumps(summary))
    return 0

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--topo", type=Path)
    p.add_argument("--out", type=Path)
    p.add_argument("--serve-fixture-hub", action="store_true")
    p.add_argument("--ready-file", type=Path)
    p.add_argument("--objects", type=int, default=20)
    p.add_argument("--scenario", choices=["ready", "missing", "stale", "auth_required", "forbidden", "rate_limited", "unavailable", "mfa"], default="ready")
    p.add_argument("--delay-seconds", type=float, default=0)
    args = p.parse_args()
    if not 0 <= args.objects <= 1001 or not 0 <= args.delay_seconds <= 60: p.error("objects must be 0..1001, delay 0..60 seconds")
    if args.serve_fixture_hub:
        if not args.ready_file or not args.ready_file.is_absolute() or args.ready_file.exists(): p.error("--ready-file must be a new absolute path")
        return serve(args)
    if not args.topo or not args.topo.is_absolute() or not args.out or not args.out.is_absolute(): p.error("--topo and --out must be absolute paths")
    return acceptance(args)

if __name__ == "__main__":
    raise SystemExit(main())
