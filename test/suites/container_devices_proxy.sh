test_container_devices_proxy() {
  container_devices_proxy_validation
  container_devices_proxy_tcp
  container_devices_proxy_tcp_unix
  container_devices_proxy_tcp_udp
  container_devices_proxy_udp
  container_devices_proxy_unix
  container_devices_proxy_unix_udp
  container_devices_proxy_unix_tcp
}

container_devices_proxy_validation() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"
  HOST_TCP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Check that connecting to a DNS name is not allowed (security risk).
  if lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:localhost:4321 bind=host ; then
    echo "Proxy device shouldn't allow connect hostnames, only IPs"
    false
  fi

  # Check using wildcard addresses isn't allowed in NAT mode.
  if lxc config device add proxyTester proxyDev proxy "listen=tcp:0.0.0.0:$HOST_TCP_PORT" connect=tcp:0.0.0.0:4321 nat=true ; then
    echo "Proxy device shouldn't allow wildcard IPv4 listen addresses in NAT mode"
    false
  fi
  if lxc config device add proxyTester proxyDev proxy "listen=tcp:[::]:$HOST_TCP_PORT" connect=tcp:0.0.0.0:4321 nat=true ; then
    echo "Proxy device shouldn't allow wildcard IPv6 listen addresses in NAT mode"
    false
  fi

  # Check using mixing IP versions in listen/connect addresses isn't allowed in NAT mode.
  if lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" "connect=tcp:[::]:4321" nat=true ; then
    echo "Proxy device shouldn't allow mixing IP address versions in NAT mode"
    false
  fi
  if lxc config device add proxyTester proxyDev proxy "listen=tcp:[::1]:$HOST_TCP_PORT" connect=tcp:0.0.0.0:4321 nat=true ; then
    echo "Proxy device shouldn't allow mixing IP address versions in NAT mode"
    false
  fi

  # Check user proxy_protocol isn't allowed in NAT mode.
  if lxc config device add proxyTester proxyDev proxy "listen=tcp:[::1]:$HOST_TCP_PORT" "connect=tcp:[::]:4321" nat=true proxy_protocol=true ; then
    echo "Proxy device shouldn't allow proxy_protocol in NAT mode"
    false
  fi

  # Check that old invalid config doesn't prevent device being stopped and removed cleanly.
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host
  lxd sql global "UPDATE instances_devices_config SET value='tcp:localhost:4321' WHERE value='tcp:127.0.0.1:4321';"
  lxc config device remove proxyTester proxyDev

  # Add the device again with the same listen param so if the old process hasn't been stopped it will fail to start.
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host

  lxc delete -f proxyTester
}

container_devices_proxy_tcp() {
  echo "====> Testing tcp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: tcp"
  HOST_TCP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the port
  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Initial test
  lxc config device remove proxyTester proxyDev
  HOST_TCP_PORT2=$(local_tcp_port)
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT,$HOST_TCP_PORT2" connect=tcp:127.0.0.1:4321-4322 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:4322 exec:/bin/cat &
  NSENTER_PID1=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true
  ECHO1=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT2}")
  kill "${NSENTER_PID1}" 2>/dev/null || true
  wait "${NSENTER_PID1}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  if [ "${ECHO1}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester

  # Try NAT
  lxc init testimage nattest

  lxc network create lxdt$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  lxc network attach lxdt$$ nattest eth0
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get lxdt$$ ipv6.address | cut -d/ -f1)00"
  lxc config device set nattest eth0 ipv4.address "${v4_addr}"
  lxc config device set nattest eth0 ipv6.address "${v6_addr}"

  firewallDriver=$(lxc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')

  lxc start nattest
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.validNAT
    ! nft -nn list chain ip lxd out.nattest.validNAT
  fi

  lxc config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234" connect="tcp:${v4_addr}:1234" bind=host
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.validNAT
    ! nft -nn list chain ip lxd out.nattest.validNAT
  fi

  # enable NAT
  lxc config device set nattest validNAT nat true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 3 ]
  else
      [ "$(nft -nn list chain ip lxd prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat to ${v4_addr}:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat to ${v4_addr}:1234")" -eq 1 ]
  fi

  lxc config device remove nattest validNAT
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.validNAT
    ! nft -nn list chain ip lxd out.nattest.validNAT
  fi

  lxc config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234-1235" connect="tcp:${v4_addr}:1234" bind=host nat=true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 5 ]
  else
      [ "$(nft -nn list chain ip lxd prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat to ${v4_addr}:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1235 dnat to ${v4_addr}:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat to ${v4_addr}:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1235 dnat to ${v4_addr}:1234")" -eq 1 ]
  fi

  lxc config device remove nattest validNAT
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.validNAT
    ! nft -nn list chain ip lxd out.nattest.validNAT
  fi

  lxc config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234-1235" connect="tcp:${v4_addr}:1234-1235" bind=host nat=true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 6 ]
  else
      [ "$(nft -nn list chain ip lxd prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat to ${v4_addr}:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1235 dnat to ${v4_addr}:1235")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat to ${v4_addr}:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip lxd out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1235 dnat to ${v4_addr}:1235")" -eq 1 ]
  fi

  lxc config device remove nattest validNAT
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.validNAT
    ! nft -nn list chain ip lxd out.nattest.validNAT
  fi

  # IPv6 test
  lxc config device add nattest validNAT proxy listen="tcp:[::1]:1234" connect="tcp:[::]:1234" bind=host nat=true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(ip6tables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 3 ]
  else
      [ "$(nft -nn list chain ip6 lxd prert.nattest.validNAT | grep -c "ip6 daddr ::1 tcp dport 1234 dnat to \[${v6_addr}\]:1234")" -eq 1 ]
      [ "$(nft -nn list chain ip6 lxd out.nattest.validNAT | grep -c "ip6 daddr ::1 tcp dport 1234 dnat to \[${v6_addr}\]:1234")" -eq 1 ]
  fi

  lxc config device unset nattest validNAT nat
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(ip6tables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip6 lxd prert.nattest.validNAT
    ! nft -nn list chain ip6 lxd out.nattest.validNAT
  fi

  lxc config device remove nattest validNAT

  # This won't enable NAT
  lxc config device add nattest invalidNAT proxy listen="tcp:127.0.0.1:1234" connect="udp:${v4_addr}:1234" bind=host
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (invalidNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.invalidNAT
    ! nft -nn list chain ip lxd out.nattest.invalidNAT
  fi

  lxc delete -f nattest
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain ip lxd prert.nattest.validNAT
    ! nft -nn list chain ip lxd out.nattest.validNAT
  fi

  lxc network delete lxdt$$
}

container_devices_proxy_unix() {
  echo "====> Testing unix proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Some busybox images don't have /tmp globally accessible.
  lxc exec proxyTester -- chmod 1777 /tmp

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" uid=1234 gid=1234 security.uid=1234 security.gid=1234 connect=unix:/tmp/"lxdtest-$(basename "${LXD_DIR}").sock" bind=host
  (
    PID="$(lxc query /1.0/containers/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  (
    PID="$(lxc query /1.0/containers/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the socket
  lxc config device set proxyTester proxyDev connect unix:/tmp/"lxdtest-$(basename "${LXD_DIR}")-2.sock"
  (
    PID="$(lxc query /1.0/containers/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}")-2.sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  lxc delete -f proxyTester
}

container_devices_proxy_tcp_unix() {
  echo "====> Testing tcp to unix proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: tcp -> unix"
  HOST_TCP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:${HOST_TCP_PORT}" connect=unix:/tmp/"lxdtest-$(basename "${LXD_DIR}").sock" bind=host
  (
    PID="$(lxc query /1.0/containers/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  lxc restart -f proxyTester
  (
    PID="$(lxc query /1.0/containers/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the socket
  lxc config device set proxyTester proxyDev connect unix:/tmp/"lxdtest-$(basename "${LXD_DIR}")-2.sock"
  (
    PID="$(lxc query /1.0/containers/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}")-2.sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester
}

container_devices_proxy_unix_tcp() {
  echo "====> Testing unix to tcp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix -> tcp"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the port
  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat tcp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  lxc delete -f proxyTester
}

container_devices_proxy_udp() {
  echo "====> Testing udp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: udp"
  HOST_UDP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=udp:127.0.0.1:$HOST_UDP_PORT" connect=udp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the port
  lxc config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester
}

container_devices_proxy_unix_udp() {
  echo "====> Testing unix to udp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix -> udp"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=udp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the port
  lxc config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  lxc delete -f proxyTester
}

container_devices_proxy_tcp_udp() {
  echo "====> Testing tcp to udp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: tcp -> udp"
  HOST_TCP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=udp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the port
  lxc config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat udp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester
}
