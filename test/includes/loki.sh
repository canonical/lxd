# Loki helpers
spawn_loki() {
  # Return if Loki is already set up.
  [ -e "${TEST_DIR}/loki.pid" ] && return

  local log_file="${1}"

  PYTHONUNBUFFERED=1 python3 -c '
import http.server
import socketserver
import sys

class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        content_len = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_len).decode("utf-8")
        # Just print the body to the log file for testing purposes.
        print(body)
        self.send_response(200)
        self.end_headers()

    def do_GET(self):
        self.send_response(200)
        self.end_headers()

    def log_message(self, format, *args):
        return

if __name__ == "__main__":
    # Allow address reuse
    socketserver.TCPServer.allow_reuse_address = True
    with socketserver.TCPServer(("127.0.0.1", 3100), Handler) as httpd:
        httpd.serve_forever()
' >> "${log_file}" 2>&1 &
  echo $! > "${TEST_DIR}/loki.pid"

  sleep 0.1
}

kill_loki() {
  [ ! -e "${TEST_DIR}/loki.pid" ] && return

  kill "$(< "${TEST_DIR}/loki.pid")"
  rm "${TEST_DIR}/loki.pid"
}
