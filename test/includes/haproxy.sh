# haproxy related test helpers.

setup_haproxy() {
  command -v haproxy > /dev/null && return
  install_tools haproxy
  systemctl disable --now haproxy.service
}

start_haproxy() {
  # Check configuration and restart service.
  haproxy -q -c -f /etc/haproxy/haproxy.cfg
  systemctl restart haproxy.service
}

stop_haproxy() {
  systemctl stop haproxy.service
}

configure_haproxy() {
  local fqdn="${1}"
  shift
  local proxy="${1}"
  shift
  local conn_rate="${1}"
  shift
  local servers="${*}"
  local i
  local backend_servers=""
  local comment="\  # LXD cluster members"
  local server_args="check"

  if [ "${proxy:-}" = "true" ]; then
    comment="${comment} with PROXY protocol and core.https_trusted_proxy"
    server_args="check send-proxy"
  fi

  i=0
  for server in ${servers}; do
    i=$((i + 1))
    backend_servers="${backend_servers}  server lxd-${i} ${server} ${server_args}\n"
  done

  # Extract the HAProxy configuration from the documentation
  sed -n '/^# HAProxy$/,/^# EOF$/ p' "${MAIN_DIR}/../doc/authentication.md" | \
    # Replace the FQDN and remove the cluster members section
    sed 's/\blxd\.example\.com\b/'"${fqdn}"'/; /^\s*# LXD cluster members\b/d; /^\s*server lxd-/d' | \
    # Update the connection rate limit
    sed '/sc_conn_rate(0)/ s/ gt [0-9]\+ }$/ gt '"${conn_rate}"' }/' | \
    # Then insert the comment and backend servers
    sed '/^# EOF$/ i '"${comment}"'\n'"${backend_servers}"''
}
