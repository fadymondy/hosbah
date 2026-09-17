import os
from http.server import BaseHTTPRequestHandler, HTTPServer

VERSION = "v1"


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"ok\n" if self.path == "/health" else f"hello from Python {VERSION} ({os.environ.get('GREETING', '')})\n".encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(body)


port = int(os.environ.get("PORT", "8000"))
print(f"listening on {port}", flush=True)
HTTPServer(("0.0.0.0", port), Handler).serve_forever()
