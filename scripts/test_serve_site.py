"""The local browser bridge keeps saved viewer secrets behind verified TLS."""
import http.client
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import ssl
import socket
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import Mock, patch

SPEC = importlib.util.spec_from_file_location("serve_site", Path(__file__).with_name("serve_site.py"))
site = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(site)


class Upstream(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        self.server.requests.append((self.path, dict(self.headers)))
        status, body = self.server.response
        self.send_response(status)
        if status == 302:
            self.send_header("Location", "https://another-collector.invalid/v1/snapshot")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        delay = getattr(self.server, "body_delay", 0)
        try:
            if delay:
                for value in body:
                    self.wfile.write(bytes([value]))
                    self.wfile.flush()
                    time.sleep(delay)
            else:
                self.wfile.write(body)
        except OSError:
            pass


class LocalViewerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.cert, cls.key = cls.root / "cert.pem", cls.root / "key.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                        "-days", "1", "-keyout", str(cls.key), "-out", str(cls.cert),
                        "-subj", "/CN=127.0.0.1", "-addext", "subjectAltName=IP:127.0.0.1"],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def setUp(self):
        self.upstream = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(self.cert, self.key)
        self.upstream.socket = tls.wrap_socket(self.upstream.socket, server_side=True)
        self.upstream.requests = []
        self.upstream.response = 200, b'{"project":"test-network"}'
        self.upstream_thread = threading.Thread(
            target=self.upstream.serve_forever, kwargs={"poll_interval": .01}, daemon=True)
        self.upstream_thread.start()
        self.credentials = self.root / "credentials.json"
        self.saved_token = "sample-viewer-token-" + "x" * 24  # Existing base64-style, 43 characters.
        self.credentials.write_text(json.dumps({"active": "test", "profiles": {"test": {
            "project": "test-network", "servers": [{
                "url": f"https://127.0.0.1:{self.upstream.server_port}",
                "token": self.saved_token, "ca_file": str(self.cert)}]}}}))
        self.credentials.chmod(0o600)
        self.collector = site.Collector(self.credentials, None)
        self.server = site.serve(site.ROOT / "site/public", self.collector, 0)
        self.thread = threading.Thread(
            target=self.server.serve_forever, kwargs={"poll_interval": .01}, daemon=True)
        self.thread.start()
        self.origin = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        for server, thread in ((self.server, self.thread), (self.upstream, self.upstream_thread)):
            server.shutdown()
            thread.join()
            server.server_close()

    def request(self, path, method="GET", headers=None, body=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.server.server_port, timeout=3)
        try:
            connection.request(method, path, headers=headers or {}, body=body)
            response = connection.getresponse()
            return response.status, dict(response.headers), response.read()
        finally:
            connection.close()

    def authorized(self, path="/v1/snapshot"):
        return self.request(site.PREFIX + "/collector" + path,
                            headers={"Authorization": "Bearer " + self.collector.session})

    def exchange(self, secret):
        return self.request(site.PREFIX + "/exchange", "POST", headers={
            "Origin": self.origin, "Sec-Fetch-Site": "same-origin",
            "Content-Type": "text/plain"}, body=secret)

    def test_browser_session_never_receives_saved_viewer_token(self):
        script = site.PREFIX + "/connect.js"
        self.assertEqual(self.request(script)[0], 403)
        status, headers, body = self.request(script, headers={
            "Sec-Fetch-Site": "same-origin", "Referer": self.origin + "/zh/app/"})
        self.assertEqual(status, 200)
        self.assertNotIn(self.saved_token.encode(), body)
        self.assertNotIn(self.collector.session.encode(), body)
        self.assertNotIn(self.collector.launch.encode(), body)
        self.assertEqual(headers["Cache-Control"], "no-store")
        self.assertEqual(headers["Cross-Origin-Resource-Policy"], "same-origin")
        status, _, body = self.exchange(self.collector.launch)
        self.assertEqual(status, 200)
        self.assertNotIn(self.saved_token.encode(), body)
        self.assertEqual(json.loads(body)["token"], self.collector.session)
        self.assertEqual(self.authorized("/v1/history?check=local&limit=10")[0], 200)
        path, sent = self.upstream.requests[-1]
        self.assertEqual(path, "/v1/history?check=local&limit=10")
        self.assertEqual(sent["Authorization"], "Bearer " + self.saved_token)
        self.assertNotIn("Cookie", sent)
        self.assertNotIn("Origin", sent)

    def test_default_origin_varies_and_fixed_ports_require_opt_in(self):
        with site.serve(site.ROOT / "site/public", self.collector) as another:
            self.assertGreater(another.server_port, 0)
            self.assertNotEqual(another.server_port, self.server.server_port)
        with self.assertRaisesRegex(ValueError, "allow-fixed-port"):
            site.serve(site.ROOT / "site/public", self.collector, 8001)
        with site.serve(site.ROOT / "site/public", self.collector) as available:
            fixed = available.server_port
        with site.serve(site.ROOT / "site/public", self.collector, fixed,
                        allow_fixed_port=True) as explicit:
            self.assertEqual(explicit.server_port, fixed)

    def test_partial_headers_are_admitted_before_threads_are_created(self):
        self.server.slots = threading.BoundedSemaphore(2)
        sockets = []
        original = self.server.process_request_thread
        started = []

        def tracked(*args):
            started.append(threading.get_ident())
            return original(*args)

        def connect():
            connection = socket.create_connection(("127.0.0.1", self.server.server_port), timeout=1)
            sockets.append(connection)
            connection.sendall(b"GET / HTTP/1.1\r\nX-Slow: ")
            return connection

        with patch.object(self.server, "process_request_thread", tracked):
            try:
                for _ in range(2):
                    connect()
                deadline = time.monotonic() + 1
                while len(started) < 2 and time.monotonic() < deadline:
                    time.sleep(.005)
                self.assertEqual(len(started), 2)
                for _ in range(5):
                    connection = connect()
                    try:
                        self.assertEqual(connection.recv(1), b"")
                    except ConnectionResetError:
                        pass
                self.assertEqual(len(started), 2)
            finally:
                for connection in sockets:
                    connection.close()

    def test_dripping_headers_cannot_extend_absolute_deadline(self):
        with patch.object(site, "HEADER_TIMEOUT", .15):
            with socket.create_connection(("127.0.0.1", self.server.server_port), timeout=1) as connection:
                connection.sendall(b"GET / HTTP/1.1\r\nX-Slow: ")
                start = time.monotonic()
                for _ in range(20):
                    time.sleep(.025)
                    try:
                        connection.sendall(b"x")
                    except OSError:
                        break
                self.assertLess(time.monotonic() - start, .5)
                try:
                    self.assertEqual(connection.recv(1), b"")
                except ConnectionResetError:
                    pass

    def test_dripping_exchange_body_cannot_hold_admission_slots(self):
        self.server.slots = threading.BoundedSemaphore(1)
        secret = self.collector.launch
        with patch.object(site, "EXCHANGE_TIMEOUT", .15, create=True):
            with socket.create_connection(("127.0.0.1", self.server.server_port), timeout=1) as connection:
                connection.sendall((f"POST {site.PREFIX}/exchange HTTP/1.1\r\n"
                                    f"Host: {self.origin.removeprefix('http://')}\r\n"
                                    f"Origin: {self.origin}\r\n"
                                    "Sec-Fetch-Site: same-origin\r\n"
                                    "Content-Length: 64\r\n\r\n").encode())
                start = time.monotonic()
                for _ in range(24):
                    time.sleep(.025)
                    try:
                        connection.sendall(b"0")
                    except OSError:
                        break
                self.assertLess(time.monotonic() - start, .5)
                try:
                    self.assertEqual(connection.recv(1), b"")
                except ConnectionResetError:
                    pass
                self.assertEqual(self.collector.launch, secret)
        self.assertEqual(self.exchange(secret)[0], 200)

    def test_dripping_collector_body_has_absolute_deadline(self):
        self.upstream.body_delay = .025
        with patch.object(site, "COLLECTOR_TIMEOUT", .15, create=True):
            start = time.monotonic()
            self.assertEqual(self.authorized()[0], 502)
            self.assertLess(time.monotonic() - start, .5)
        self.upstream.body_delay = 0
        self.assertEqual(self.authorized()[0], 200)

    def test_forged_browser_headers_cannot_acquire_a_capability(self):
        forged = {"Host": self.origin.removeprefix("http://"), "Origin": self.origin,
                  "Sec-Fetch-Site": "same-origin", "Referer": self.origin + "/zh/app/"}
        for path in (site.PREFIX + "/connect.js", "/zh/app/", "/app/"):
            for method in ("GET", "HEAD"):
                status, _, body = self.request(path, method, headers=forged)
                self.assertEqual(status, 200)
                self.assertNotIn(self.collector.session.encode(), body)
                self.assertNotIn(self.collector.launch.encode(), body)
                self.assertNotIn(self.saved_token.encode(), body)
        self.assertEqual(self.exchange("0" * 64)[0], 401)
        self.assertEqual(self.request(site.PREFIX + "/exchange", headers=forged)[0], 404)
        self.assertEqual(self.request(site.PREFIX + "/collector/v1/config", headers=forged)[0], 401)
        self.assertEqual(self.upstream.requests, [])

    def test_launch_exchange_expires_and_is_atomic_one_time(self):
        secret = self.collector.launch
        with ThreadPoolExecutor(max_workers=2) as clients:
            statuses = list(clients.map(lambda _: self.exchange(secret)[0], range(2)))
        self.assertEqual(sorted(statuses), [200, 401])
        self.assertEqual(self.exchange(secret)[0], 401)
        self.assertEqual(self.authorized()[0], 200)

    def test_expired_launch_and_invalid_exchanges_fail_closed(self):
        secret = self.collector.launch
        path = site.PREFIX + "/exchange"
        self.assertEqual(self.request(path, "POST", body=secret)[0], 403)
        self.assertEqual(self.request(path, "PUT", body=secret)[0], 405)
        self.assertEqual(self.exchange("x" * 64)[0], 400)
        self.assertEqual(self.exchange("f" * 257)[0], 400)
        with patch.object(site.time, "monotonic", return_value=self.collector.launch_expires):
            self.assertEqual(self.exchange(secret)[0], 401)
        self.assertEqual(self.upstream.requests, [])

    def test_bridge_rejects_unauthenticated_reads_writes_and_other_origins(self):
        path = site.PREFIX + "/collector/v1/snapshot"
        self.assertEqual(self.request(path)[0], 401)
        self.assertEqual(self.request(path, headers={
            "Authorization": "Bearer " + self.saved_token})[0], 401)
        self.assertEqual(self.request(path, "POST")[0], 405)
        self.assertEqual(self.authorized("/v1/ingest")[0], 404)
        self.assertEqual(self.request(path, headers={"Host": "attacker.invalid"})[0], 403)
        self.assertEqual(self.request(path, headers={"Origin": "https://attacker.invalid"})[0], 403)
        self.assertEqual(self.upstream.requests, [])

    def test_upstream_certificate_is_verified(self):
        self.assertEqual(self.authorized()[0], 200)
        self.upstream.requests.clear()
        self.collector.tls = ssl.create_default_context()
        self.assertEqual(self.authorized()[0], 502)
        self.assertEqual(self.upstream.requests, [])

    def test_redirects_project_mismatch_and_oversized_responses_fail_closed(self):
        self.upstream.response = 302, b'{}'
        status, headers, _ = self.authorized()
        self.assertEqual(status, 502)
        self.assertNotIn("Location", headers)
        self.upstream.response = 200, b'{"project":"different-network"}'
        for path in ("/v1/config", "/v1/snapshot", "/v1/history?check=local", "/v1/rollups?check=local"):
            with self.subTest(path=path):
                self.assertEqual(self.authorized(path)[0], 502)
        self.upstream.response = 200, b'x' * 2048
        with patch.object(site, "MAX_BODY", 1024):
            self.assertEqual(self.authorized()[0], 502)

    def test_local_connection_is_only_injected_in_app_and_worker_policy_is_preserved(self):
        status, headers, body = self.request("/zh/app/")
        self.assertEqual(status, 200)
        self.assertIn((site.PREFIX + "/connect.js").encode(), body)
        self.assertNotIn(self.collector.session.encode(), body)
        self.assertNotIn((site.PREFIX + "/connect.js").encode(), self.request("/zh/demo/")[2])
        self.assertIn("connect-src 'self'", headers["Content-Security-Policy"])
        worker = next((site.ROOT / "site/public/js").glob("collector-worker.*.js"))
        status, headers, _ = self.request("/js/" + worker.name)
        self.assertEqual(status, 200)
        self.assertIn("connect-src 'self' https:", headers["Content-Security-Policy"])


class SocketDeadlineTests(unittest.TestCase):
    def test_callback_started_before_cancel_cannot_close_completed_read(self):
        class Timer:
            def __init__(self, timeout, callback):
                self.callback = callback

            def start(self):
                pass

            def cancel(self):
                self.callback()

        peer = Mock()
        with patch.object(site.threading, "Timer", Timer):
            with site.socket_deadline(peer, 60):
                pass
        peer.shutdown.assert_not_called()

    def test_expired_callback_cannot_admit_a_read_that_completed_concurrently(self):
        class Timer:
            def __init__(self, timeout, callback):
                self.callback = callback

            def start(self):
                self.callback()

            def cancel(self):
                pass

        peer = Mock()
        with patch.object(site.threading, "Timer", Timer):
            with self.assertRaises(TimeoutError):
                with site.socket_deadline(peer, 60):
                    pass
        peer.shutdown.assert_called_once_with(socket.SHUT_RDWR)


if __name__ == "__main__":
    unittest.main()
