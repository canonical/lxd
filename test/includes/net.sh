# Network-related helper functions.

# Return an available random local port
local_tcp_port() {
    exec python3 -c "
import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.bind(('127.0.0.1', 0))
print(s.getsockname()[1])
s.close()"
}

# Certificate-aware curl wrapper
my_curl() {
    local CERTNAME="${CERTNAME:-"client"}"
    curl --insecure --silent --cert "${LXD_CONF}/${CERTNAME}.crt" --key "${LXD_CONF}/${CERTNAME}.key" "$@"
}

# Certificate-aware curl wrapper with strict TLS verification.
trusted_curl() {
    local CERTNAME="${CERTNAME:-"client"}"
    local CACERT="${CACERT:-"${LXD_CONF}/server.crt"}"
    curl --silent --cert "${LXD_CONF}/${CERTNAME}.crt" --key "${LXD_CONF}/${CERTNAME}.key" --cacert "${CACERT}" "$@"
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

  while eval "$cmd" | grep -wFm1 "tentative" ; do
    sleep 0.1
  done
}
