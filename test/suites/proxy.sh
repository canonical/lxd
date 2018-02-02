test_proxy_device() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  MESSAGE="Proxy device test string"
  HOST_TCP_PORT=$(local_tcp_port)

  lxc launch testimage proxyTester
  lxc config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f proxyTest.out

  lxc restart -f proxyTester
  nsenter -n -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f proxyTest.out

  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -t "$(lxc query /1.0/containers/proxyTester/state | jq .pid)" -- nc -6 -l 1337 > proxyTest.out &
  sleep 2

  echo "${MESSAGE}" | nc -w1 127.0.0.1 "${HOST_TCP_PORT}"

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f proxyTest.out
  lxc delete -f proxyTester
}

