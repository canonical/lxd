test_proxy_device() {
  test_proxy_device_tcp
  test_proxy_device_tcp_unix
  test_proxy_device_tcp_udp
  test_proxy_device_udp
  test_proxy_device_unix
  test_proxy_device_unix_udp
  test_proxy_device_unix_tcp
}

test_proxy_device_tcp() {
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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true
  ECHO1=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT2}")
  kill -9 "${NSENTER_PID1}" 2>/dev/null || true

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

  lxc network create lxdt$$ dns.domain=test dns.mode=managed
  lxc network attach lxdt$$ nattest eth0
  v4_addr="$(lxc network get lxdt$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get lxdt$$ ipv6.address | cut -d/ -f1)00"
  lxc config device set nattest eth0 ipv4.address "${v4_addr}"
  lxc config device set nattest eth0 ipv6.address "${v6_addr}"

  lxc start nattest
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  lxc config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234" connect="tcp:${v4_addr}:1234" bind=host
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  # enable NAT
  lxc config device set nattest validNAT nat true
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 2 ]

  lxc config device remove nattest validNAT
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  lxc config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234-1235" connect="tcp:${v4_addr}:1234" bind=host nat=true
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 4 ]

  lxc config device remove nattest validNAT
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  lxc config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234-1235" connect="tcp:${v4_addr}:1234-1235" bind=host nat=true
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 4 ]

  lxc config device remove nattest validNAT
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  # IPv6 test
  lxc config device add nattest validNAT proxy listen="tcp:[::1]:1234" connect="tcp:[::]:1234" bind=host nat=true
  [ "$(ip6tables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 2 ]

  lxc config device unset nattest validNAT nat
  [ "$(ip6tables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  lxc config device remove nattest validNAT

  # This won't enable NAT
  lxc config device add nattest invalidNAT proxy listen="tcp:127.0.0.1:1234" connect="udp:${v4_addr}:1234" bind=host
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (invalidNAT)")" -eq 0 ]

  lxc delete -f nattest
  [ "$(iptables -w -t nat -S | grep -c "generated for LXD container nattest (validNAT)")" -eq 0 ]

  lxc network delete lxdt$$
}

test_proxy_device_unix() {
  echo "====> Testing unix proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=unix:/tmp/"lxdtest-$(basename "${LXD_DIR}").sock" bind=host
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the socket
  lxc config device set proxyTester proxyDev connect unix:/tmp/"lxdtest-$(basename "${LXD_DIR}")-2.sock"
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}")-2.sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_tcp_unix() {
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
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  lxc restart -f proxyTester
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the socket
  lxc config device set proxyTester proxyDev connect unix:/tmp/"lxdtest-$(basename "${LXD_DIR}")-2.sock"
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- socat unix-listen:"lxdtest-$(basename "${LXD_DIR}")-2.sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_unix_tcp() {
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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_udp() {
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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_unix_udp() {
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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - unix:"${HOST_SOCK#$(pwd)/}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_tcp_udp() {
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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

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

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.1) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill -9 "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  lxc delete -f proxyTester
}
