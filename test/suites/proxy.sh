test_proxy_device() {
  test_proxy_device_tcp
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

  # Cleanup
  lxc delete -f proxyTester
}
