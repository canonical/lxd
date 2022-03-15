# Network-related helper functions.

# Return an available random local port
local_tcp_port() {
    if command -v python3 >/dev/null 2>&1; then
        (
            cat << EOF
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
EOF
        ) | python3
        return
    fi

    # shellcheck disable=SC2039
    local port pid

    while true; do
        port=$(shuf -i 10000-32768 -n 1)
        nc -l 127.0.0.1 "${port}" >/dev/null 2>&1 &
        pid=$!
        kill "${pid}" >/dev/null 2>&1 || continue
        wait "${pid}" || true
        echo "${port}"
        return
    done
}

# Certificate-aware curl wrapper
my_curl() {
    curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" "$@"
}

# Wait for duplicate address detection to complete.
# Usage: Either "wait_for_dad <device>" or "wait_for_dad <container> <device>".
wait_for_dad() {
  cmd="ip -6 a show -tentative dev $1"
  if [ "$#" -eq 2 ]; then
    cmd="lxc exec $1 -- ip -6 a show -tentative dev $2"
  fi

  # Ensure the command succeeds (else the while loop will break for the wrong reason).
  if ! eval "$cmd"; then
    echo "Invalid arguments to wait_for_dad"
    false
    return
  fi

  while true
  do
    ip -6 a show
    # Stop waiting as soon as non-tentative address(es) show up
    if [ -n "$(eval "$cmd")" ]; then
      break
    fi

    sleep 0.5
  done
}
