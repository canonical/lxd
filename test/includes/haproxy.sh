# haproxy related test helpers.

setup_haproxy() {
  command -v haproxy > /dev/null && return
  install_tools haproxy
}

start_haproxy() {
  # Check configuration and restart service.
  haproxy -q -c -f /etc/haproxy/haproxy.cfg
  systemctl restart haproxy
}

stop_haproxy() {
  systemctl stop haproxy
}

configure_haproxy() {
  local fqdn="${1}"
  shift
  local servers="${*}"
  local backend_servers

  i=0
  for server in ${servers}; do
    i=$((i + 1))
    backend_servers+="  server lxd-${i} ${server} check send-proxy\n"
  done

  sed -e 's|@@BACKEND_SERVERS@@|'"${backend_servers}"'|' \
      -e 's|@@FQDN@@|'"${fqdn}"'|' \
      "${MAIN_DIR}/deps/haproxy.cfg.tpl" > /etc/haproxy/haproxy.cfg
}
