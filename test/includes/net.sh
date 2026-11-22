# Network-related helper functions.

# Return an available random local port
local_tcp_port() {
    if command -v python3 >/dev/null 2>&1; then
        cat << EOF | python3
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()
EOF
        return
    fi

    # shellcheck disable=SC2039,3043
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

# waitTCPPort: poll host:port until it reaches the desired state, checking up
# to <attempts> times (default 10) every <interval> seconds (default 0.1).
# Uses bash's own /dev/tcp redirection so no external tool (nc, etc.) is needed.
# Usage: waitTCPPort <host> <port> [open|closed] [attempts] [interval]
# ==> Returns 0 once the desired state is observed, 1 otherwise.
waitTCPPort() {
    local host="${1}"
    local port="${2}"
    local state="${3:-open}"
    local attempts="${4:-10}"
    local interval="${5:-0.1}"

    for _ in $(seq "${attempts}"); do
        if (exec 3<>"/dev/tcp/${host}/${port}") 2>/dev/null; then
            exec 3<&- 3>&-
            [ "${state}" = "open" ] && return 0
        else
            [ "${state}" = "closed" ] && return 0
        fi
        sleep "${interval}"
    done

    return 1
}

# Certificate-aware curl wrapper
my_curl() {
    curl -k -s --cert "${LXD_CONF}/client.crt" --key "${LXD_CONF}/client.key" "$@"
}

# Wait for duplicate address detection to complete.
# Usage: Either "wait_for_dad <device>" or "wait_for_dad <container> <device>".
wait_for_dad() {
  cmd="ip -6 a show dev $1"
  if [ "$#" -eq 2 ]; then
    cmd="lxc exec $1 -- ip -6 a show dev $2"
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
    if ! eval "$cmd" | grep "tentative" ; then
      break
    fi

    sleep 0.5
  done
}
