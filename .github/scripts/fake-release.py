"""A stand-in for GitHub's releases API, for the update tests in
.github/workflows/installers.yml (ADR 0033): it says the files in a folder
are the newest release, version argv[3], and serves them.

    python3 fake-release.py <folder> <port> <version>
"""
import http.server
import json
import os
import sys

root, port, version = sys.argv[1], int(sys.argv[2]), sys.argv[3]


class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=root, **kwargs)

    def do_GET(self):
        if self.path == "/releases/latest":
            body = json.dumps({
                "tag_name": "v" + version,
                "html_url": "https://github.com/akamachinecode/exhale/releases",
                "body": "* **update:** a test release",
                "published_at": "2026-01-01T00:00:00Z",
                "assets": [
                    {"name": n, "browser_download_url": f"http://127.0.0.1:{port}/{n}"}
                    for n in os.listdir(root)
                ],
            }).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        super().do_GET()


http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
