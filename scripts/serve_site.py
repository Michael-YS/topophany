#!/usr/bin/env python3
"""Preview the site against an existing collector, using local viewer credentials.

This loopback-only development bridge verifies the collector's TLS certificate
with the saved CA. It exposes only viewer GETs, substitutes a per-process token,
and never sends the saved collector token to the browser. Production /app keeps
connecting directly to collectors; this script changes no published files.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
from functools import partial
import fnmatch
import http.client
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import secrets
import socket
import ssl
import stat
import sys
import threading
import time
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
PREFIX = "/__topophany_local__"
READS = {"/v1/config", "/v1/snapshot", "/v1/history", "/v1/rollups"}
MAX_BODY = 4 << 20
LAUNCH_TTL = 300
MAX_CONNECTIONS = 16
HEADER_TIMEOUT = 3
EXCHANGE_TIMEOUT = 3
COLLECTOR_TIMEOUT = 8


@contextmanager
def socket_deadline(peer, timeout):
    """Bound a complete read, including peers that continually send small chunks."""
    if timeout <= 0:
        raise TimeoutError("read deadline exceeded")
    deadline = time.monotonic() + timeout
    lock = threading.Lock()
    active, expired = True, False

    def expire():
        nonlocal expired
        with lock:
            if not active:
                return
            expired = True
            try:
                peer.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    timer = threading.Timer(timeout, expire)
    timer.daemon = True
    timer.start()
    try:
        yield
    finally:
        with lock:
            active = False
            timed_out = expired or time.monotonic() >= deadline
        timer.cancel()
    if timed_out:
        raise TimeoutError("read deadline exceeded")


class BoundedHTTPServer(ThreadingHTTPServer):
    """Acquire admission before spawning; partial headers have a total deadline."""
    request_queue_size = MAX_CONNECTIONS

    def __init__(self, *args, **kwargs):
        self.slots = threading.BoundedSemaphore(MAX_CONNECTIONS)
        self.header_timers = {}
        self.timer_lock = threading.Lock()
        super().__init__(*args, **kwargs)

    def process_request(self, request, client_address):
        if not self.slots.acquire(blocking=False):
            self.shutdown_request(request)
            return
        timer = threading.Timer(HEADER_TIMEOUT, self.expire_headers, args=(request,))
        timer.daemon = True
        with self.timer_lock:
            self.header_timers[request] = (timer, time.monotonic() + HEADER_TIMEOUT)
        try:
            request.settimeout(HEADER_TIMEOUT)
            timer.start()
            super().process_request(request, client_address)
        except BaseException:
            self.headers_complete(request)
            self.slots.release()
            raise

    def expire_headers(self, request):
        # An idle timeout alone lets a slow sender reset the clock forever.
        with self.timer_lock:
            if request not in self.header_timers:
                return
            try:
                request.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    def headers_complete(self, request):
        with self.timer_lock:
            entry = self.header_timers.pop(request, None)
        if entry is not None:
            timer, deadline = entry
            timer.cancel()
            return time.monotonic() < deadline
        return False

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.headers_complete(request)
            self.slots.release()


class Collector:
    def __init__(self, credentials: Path, profile: str | None):
        if stat.S_IMODE(credentials.stat().st_mode) & 0o077:
            raise ValueError("viewer credentials must be private (mode 0600)")
        saved = json.loads(credentials.read_text())
        entry = saved["profiles"][profile or saved["active"]]
        self.project = entry["project"]
        endpoint = entry["servers"][0]
        self.url = endpoint["url"]
        self.target = urlsplit(self.url)
        if (self.target.scheme != "https" or not self.target.hostname
                or self.target.username is not None or self.target.password is not None
                or self.target.query or self.target.fragment):
            raise ValueError("the local bridge requires a fixed HTTPS collector URL")
        self.token = endpoint.get("token", "")
        if not re.fullmatch(r"[!-~]{32,512}", self.token):
            raise ValueError("the profile needs an explicit viewer token")
        self.tls = ssl.create_default_context(cafile=endpoint.get("ca_file") or None)
        self.session = secrets.token_hex(32)
        # Loopback and browser headers do not authenticate an OS user. Only
        # the terminal launch link conveys this one-time capability.
        self.launch = secrets.token_hex(32)
        self.launch_expires = time.monotonic() + LAUNCH_TTL
        self.launch_lock = threading.Lock()

    def exchange(self, launch: str) -> bool:
        with self.launch_lock:
            if (not self.launch or time.monotonic() >= self.launch_expires
                    or not secrets.compare_digest(launch, self.launch)):
                return False
            self.launch = ""
            return True

    def read(self, path: str) -> tuple[int, bytes]:
        started = time.monotonic()
        connection = http.client.HTTPSConnection(
            self.target.hostname, self.target.port or 443, context=self.tls,
            timeout=COLLECTOR_TIMEOUT)
        try:
            # No cookies, browser headers, or redirects reach another host.
            connection.connect()
            remaining = COLLECTOR_TIMEOUT - (time.monotonic() - started)
            with socket_deadline(connection.sock, remaining):
                connection.request("GET", self.target.path.rstrip("/") + path,
                                   headers={"Authorization": "Bearer " + self.token,
                                            "Accept": "application/json"})
                response = connection.getresponse()
                body = response.read(MAX_BODY + 1)
            if len(body) > MAX_BODY:
                raise ValueError("collector response exceeds the viewer body limit")
            if 300 <= response.status < 400:
                raise ValueError("collector redirects are refused")
            if response.status == 200 and urlsplit(path).path in ("/v1/config", "/v1/snapshot", "/v1/history", "/v1/rollups"):
                if json.loads(body).get("project") != self.project:
                    raise ValueError("collector project does not match the saved profile")
            return response.status, body
        finally:
            connection.close()


def asset_headers(directory: Path, path: str) -> dict[str, str]:
    """Apply the same header overrides as the static host, including worker CSP."""
    headers = {}
    applies = False
    for line in (directory / "_headers").read_text().splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if not line[0].isspace():
            applies = fnmatch.fnmatchcase(path, line.strip())
        elif applies:
            value = line.strip()
            if value.startswith("! "):
                headers.pop(value[2:].strip(), None)
            else:
                name, content = value.split(":", 1)
                headers[name] = content.strip()
    return headers


class SiteHandler(SimpleHTTPRequestHandler):
    server_version = "TopophanyLocal"

    def parse_request(self):
        try:
            valid = super().parse_request()
        finally:
            timely = self.server.headers_complete(self.connection)
            # Each accepted connection has one request and one admission slot.
            self.close_connection = True
        return valid and timely

    def handle(self):
        try:
            super().handle()
        except (ConnectionError, TimeoutError):
            # A deadline or disconnected local client needs no traceback.
            pass

    @property
    def origin(self):
        return f"http://127.0.0.1:{self.server.server_port}"

    def log_message(self, *_):
        # Polling should not flood the console; never log credential inputs.
        pass

    def end_headers(self):
        for key, value in asset_headers(Path(self.directory), urlsplit(self.path).path).items():
            if key != "Cache-Control":
                self.send_header(key, value)
        self.send_header("Cache-Control", "no-store")
        self.send_header("Cross-Origin-Resource-Policy", "same-origin")
        super().end_headers()

    def reply(self, status: int, body: bytes, content_type="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def guard(self) -> bool:
        if self.headers.get_all("Host") != [self.origin.removeprefix("http://")]:
            self.reply(403, b'{"error":"loopback_host_required"}')
            return False
        if self.headers.get("Origin", self.origin) != self.origin:
            self.reply(403, b'{"error":"same_origin_required"}')
            return False
        return True

    def do_HEAD(self):
        self.do_GET()

    def do_GET(self):
        if not self.guard():
            return
        path = urlsplit(self.path).path
        if path == PREFIX + "/connect.js":
            # An outside page must not load this script or acquire the session key.
            referer = urlsplit(self.headers.get("Referer", ""))
            if (self.headers.get("Sec-Fetch-Site") != "same-origin"
                    or f"{referer.scheme}://{referer.netloc}" != self.origin):
                self.reply(403, b'{"error":"same_origin_script_required"}')
                return
            self.reply(200, self.connection_script(), "text/javascript; charset=utf-8")
            return
        if path.startswith(PREFIX + "/collector/"):
            api_path = path.removeprefix(PREFIX + "/collector")
            if api_path not in READS or len(self.path) > 4096:
                self.reply(404, b'{"error":"viewer_route_required"}')
                return
            expected = "Bearer " + self.server.collector.session
            if not secrets.compare_digest(self.headers.get("Authorization", ""), expected):
                self.reply(401, b'{"error":"local_viewer_token_required"}')
                return
            try:
                request_path = self.path.removeprefix(PREFIX + "/collector")
                status, body = self.server.collector.read(request_path)
            except (OSError, ValueError, http.client.HTTPException):
                self.reply(502, b'{"error":"collector_read_failed"}')
                return
            self.reply(status, body)
            return
        if path.startswith(PREFIX):
            self.reply(404, b'{"error":"unknown_local_route"}')
            return
        file = Path(self.translate_path(self.path)).resolve()
        if not file.is_relative_to(Path(self.directory).resolve()):
            self.reply(404, b'{}')
            return
        if file.is_dir() and not path.endswith("/"):
            super().do_GET()
            return
        if file.is_dir():
            file = file / "index.html"
        if file.name == "index.html" and path.rstrip("/").endswith("/app") and file.is_file():
            script = f'<script type="module" src="{PREFIX}/connect.js"></script>'
            html = file.read_text().replace("</head>", script + "\n</head>")
            self.reply(200, html.encode(), "text/html; charset=utf-8")
            return
        super().do_GET()

    def list_directory(self, _):
        self.reply(404, b'{}')
        return None

    def do_POST(self):
        if not self.guard():
            return
        if self.path != PREFIX + "/exchange":
            self.reply(405, b'{"error":"read_only"}')
            return
        if (self.headers.get("Origin") != self.origin
                or self.headers.get("Sec-Fetch-Site") != "same-origin"):
            self.reply(403, b'{"error":"same_origin_required"}')
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 1 <= length <= 256 or self.headers.get("Transfer-Encoding"):
                raise ValueError("invalid launch length")
            self.connection.settimeout(EXCHANGE_TIMEOUT)
            with socket_deadline(self.connection, EXCHANGE_TIMEOUT):
                launch = self.rfile.read(length).decode("ascii")
            if not re.fullmatch(r"[0-9a-f]{64}", launch):
                raise ValueError("invalid launch secret")
        except (ValueError, OSError):
            self.reply(400, b'{"error":"invalid_launch"}')
            return
        if not self.server.collector.exchange(launch):
            self.reply(401, b'{"error":"launch_expired_or_used"}')
            return
        self.reply(200, json.dumps({"server": self.origin + PREFIX + "/collector",
                                    "project": self.server.collector.project,
                                    "token": self.server.collector.session}).encode())

    def do_reject(self):
        self.reply(405, b'{"error":"read_only"}')

    do_PUT = do_PATCH = do_DELETE = do_OPTIONS = do_reject

    def connection_script(self) -> bytes:
        # Public script: neither saved credentials nor either capability is
        # embedded here. Fragments are never sent in HTTP requests/referrers.
        return ("""
const launch = new URLSearchParams(location.hash.slice(1)).get('launch');
history.replaceState(null, '', location.pathname + location.search);
if (!/^[0-9a-f]{64}$/.test(launch || '')) throw new Error('Open the terminal launch link.');
const response = await fetch('""" + PREFIX + """/exchange', {
  method: 'POST', headers: {'Content-Type': 'text/plain'}, body: launch,
  credentials: 'omit', cache: 'no-store', redirect: 'error'
});
if (!response.ok) throw new Error('Restart the local viewer for a new launch link.');
const profile = await response.json();
const form = document.querySelector('#collector-connect');
function fill() {
  for (const [key, value] of Object.entries(profile)) {
    form.querySelector(`[name="${key}"]`).value = value;
  }
}
fill();
form.querySelector('[type="submit"]').addEventListener('click', () => {
  if (form.querySelector('[name="server"]').value === profile.server &&
      !form.querySelector('[name="token"]').value) fill();
});
form.requestSubmit();
""").encode()


def serve(directory: Path, collector: Collector, port: int = 0, *,
          allow_fixed_port: bool = False) -> ThreadingHTTPServer:
    if port != 0 and not allow_fixed_port:
        raise ValueError("fixed ports require --allow-fixed-port (persistent browser origin risk)")
    server = BoundedHTTPServer(("127.0.0.1", port), partial(SiteHandler, directory=str(directory)))
    server.collector = collector
    return server


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--credentials", type=Path,
                        default=Path.home() / ".config/topophany/credentials.json")
    parser.add_argument("--profile")
    parser.add_argument("--port", type=int, default=0,
                        help="random per-launch port by default")
    parser.add_argument("--allow-fixed-port", action="store_true",
                        help="allow a reusable browser origin with reduced isolation")
    parser.add_argument("--directory", type=Path, default=ROOT / "site/public")
    args = parser.parse_args()
    if args.port and not args.allow_fixed_port:
        parser.error("--port requires --allow-fixed-port; prefer the random default origin")
    try:
        collector = Collector(args.credentials, args.profile)
        status, _ = collector.read("/v1/snapshot")
        if status != 200:
            raise ValueError(f"collector rejected the viewer profile (HTTP {status})")
        with serve(args.directory, collector, args.port,
                   allow_fixed_port=args.allow_fixed_port) as server:
            print(f"Viewer (one use, expires in 5 minutes): http://127.0.0.1:{server.server_port}/zh/app/#launch={collector.launch}", flush=True)
            print(f"Collector: {collector.url} · project {collector.project}", flush=True)
            server.serve_forever()
    except KeyboardInterrupt:
        return 0
    except (OSError, ValueError, KeyError, http.client.HTTPException) as error:
        print(f"Cannot start local viewer: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
