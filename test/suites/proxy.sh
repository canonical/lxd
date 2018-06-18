test_proxy_device() {
  test_proxy_device_tcp
  test_proxy_device_udp
  test_proxy_device_unix_udp
  test_proxy_device_unix
  test_proxy_device_tcp_unix
  test_proxy_device_unix_tcp
  test_proxy_device_tcp_udp
}

test_proxy_device_tcp() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  HOST_TCP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f proxyTest.out

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f proxyTest.out

  # Change the port
  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 1337 > proxyTest.out &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f proxyTest.out

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_unix() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  OUTFILE="${TEST_DIR}/proxyTest.out"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=unix:/tmp/"lxdtest-$(basename "${LXD_DIR}").sock" bind=host
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    rm -f "lxdtest-$(basename "${LXD_DIR}").sock"
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -U -l "lxdtest-$(basename "${LXD_DIR}").sock" > "${OUTFILE}"
  ) &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK}"

  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    rm -f "lxdtest-$(basename "${LXD_DIR}").sock"
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -U -l "lxdtest-$(basename "${LXD_DIR}").sock" > "${OUTFILE}"
  ) &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK}"

  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Change the socket
  lxc config device set proxyTester proxyDev connect unix:/tmp/"lxdtest-$(basename "${LXD_DIR}")-2.sock"
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    rm -f "lxdtest-$(basename "${LXD_DIR}")-2.sock"
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -U -l "lxdtest-$(basename "${LXD_DIR}")-2.sock" > "${OUTFILE}"
  ) &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK}"

  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_tcp_unix() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  HOST_TCP_PORT=$(local_tcp_port)
  OUTFILE="${TEST_DIR}/proxyTest.out"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:${HOST_TCP_PORT}" connect=unix:/tmp/"lxdtest-$(basename "${LXD_DIR}").sock" bind=host
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    rm -f "lxdtest-$(basename "${LXD_DIR}").sock"
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -U -l "lxdtest-$(basename "${LXD_DIR}").sock" > "${OUTFILE}"
  ) &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${OUTFILE}"

  # Restart the container
  lxc restart -f proxyTester
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    rm -f "lxdtest-$(basename "${LXD_DIR}").sock"
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -U -l "lxdtest-$(basename "${LXD_DIR}").sock" > "${OUTFILE}"
  ) &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${OUTFILE}"

  # Change the socket
  lxc config device set proxyTester proxyDev connect unix:/tmp/"lxdtest-$(basename "${LXD_DIR}")-2.sock"
  (
    cd "${LXD_DIR}/containers/proxyTester/rootfs/tmp/" || exit
    umask 0000
    rm -f "lxdtest-$(basename "${LXD_DIR}")-2.sock"
    exec nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -U -l "lxdtest-$(basename "${LXD_DIR}")-2.sock" > "${OUTFILE}"
  ) &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${OUTFILE}"

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_unix_tcp() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  OUTFILE="${TEST_DIR}/proxyTest.out"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 4321 > "${OUTFILE}" &
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK#$(pwd)/}"

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 4321 > "${OUTFILE}" &
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK#$(pwd)/}"

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Change the port
  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 1337 > "${OUTFILE}" &
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK#$(pwd)/}"

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_udp() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  HOST_UDP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=udp:127.0.0.1:$HOST_UDP_PORT" connect=udp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 4321 > proxyTest.out &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -u -w1 127.0.0.1 "${HOST_UDP_PORT}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f proxyTest.out

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 4321 > proxyTest.out &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -u -w1 127.0.0.1 "${HOST_UDP_PORT}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f proxyTest.out

  # Change the port
  lxc config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 1337 > proxyTest.out &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -u -w1 127.0.0.1 "${HOST_UDP_PORT}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f proxyTest.out

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_unix_udp() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  OUTFILE="${TEST_DIR}/proxyTest.out"
  HOST_SOCK="${TEST_DIR}/lxdtest-$(basename "${LXD_DIR}")-host.sock"
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=udp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 4321 > "${OUTFILE}" &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK#$(pwd)/}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 4321 > "${OUTFILE}" &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK#$(pwd)/}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Change the port
  lxc config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 1337 > "${OUTFILE}" &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -U -w1 "${HOST_SOCK#$(pwd)/}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat "${OUTFILE}")" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${OUTFILE}" "${HOST_SOCK}"

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}

test_proxy_device_tcp_udp() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup
  MESSAGE="Proxy device test string"
  HOST_TCP_PORT=$(local_tcp_port)
  lxc launch testimage proxyTester

  # Initial test
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=udp:127.0.0.1:4321 bind=host
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 4321 > proxyTest.out &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f proxyTest.out

  # Restart the container
  lxc restart -f proxyTester
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 4321 > proxyTest.out &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f proxyTest.out

  # Change the port
  lxc config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  nsenter -n -U -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -u -l 1337 > proxyTest.out &
  NSENTER_PID=$!
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  kill -9 "${NSENTER_PID}"
  if [ -n "${NSENTER_PID}" ]; then
    kill -9 "${NSENTER_PID}" || true
  fi

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    cat "${LXD_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f proxyTest.out

  # Give the procies some time to shut down
  sleep 3

  # Cleanup
  lxc delete -f proxyTester
}
