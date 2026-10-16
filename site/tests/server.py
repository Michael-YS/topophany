"""Loopback-only browser regression fixture; uses synthetic credentials only."""
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import sys
import threading
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from serve_site import asset_headers

stats = {'slow': 0, 'canceled': 0, 'redirected': 0, 'cookies': 0}


class Handler(SimpleHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def end_headers(self):
        for key, value in asset_headers(ROOT / 'site/public', urlsplit(self.path).path).items():
            self.send_header(key, value)
        self.send_header('Access-Control-Allow-Origin', '*')
        super().end_headers()

    def do_OPTIONS(self):
        self.send_response(204)
        self.send_header('Access-Control-Allow-Methods', 'GET')
        self.send_header('Access-Control-Allow-Headers', 'Authorization')
        self.end_headers()

    def do_GET(self):
        path = urlsplit(self.path).path
        if path == '/stats':
            body = json.dumps(stats).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if path.startswith(('/slow/', '/redirect/', '/target/')):
            if self.headers.get('Cookie'):
                stats['cookies'] += 1
            if path.startswith('/slow/'):
                stats['slow'] += 1
                if stats['slow'] == 1:
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', '100000')
                    self.end_headers()
                    self.wfile.write(b'{')
                    self.wfile.flush()
                    self.connection.settimeout(20)
                    try:
                        if self.rfile.read(1) == b'':
                            stats['canceled'] += 1
                    except OSError:
                        pass
                    return
            if path.startswith('/redirect/'):
                self.send_response(302)
                self.send_header('Location', '/target/v1/snapshot')
            else:
                if path.startswith('/target/'):
                    stats['redirected'] += 1
                self.send_response(401)
            self.send_header('Content-Length', '0')
            self.end_headers()
            return
        super().do_GET()


def server():
    return ThreadingHTTPServer(('127.0.0.1', 0), partial(Handler, directory=str(ROOT / 'site/public')))


if __name__ == '__main__':
    # Different ports exercise real CORS and the collector worker's CSP.
    with server() as site, server() as collector:
        threading.Thread(target=collector.serve_forever, daemon=True).start()
        print(json.dumps({'site': site.server_port, 'collector': collector.server_port}), flush=True)
        site.serve_forever()
