test_proxy_device() {
  
  MESSAGE="Proxy device test string"
  LOCAL_TCP_PORT=1234

  lxc launch testimage proxyTester
  lxc config device add proxyTester proxyDev proxy listen=tcp:127.0.0.1:${LOCAL_TCP_PORT} connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -t $(lxc query /1.0/containers/proxyTester/state | jq .pid) -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  echo ${MESSAGE} | nc 127.0.0.1 ${LOCAL_TCP_PORT} &
  sleep 1

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f proxyTest.out

  lxc restart proxyTester
  if [ $(lxc config device list proxyTester) != "proxyDev" ]; then
    echo "Proxy device should not be removed on container restart"
    false
  fi

  nsenter -n -t $(lxc query /1.0/containers/proxyTester/state | jq .pid) -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  echo ${MESSAGE} | nc 127.0.0.1 ${LOCAL_TCP_PORT} &
  sleep 1

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f proxyTest.out

  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -t $(lxc query /1.0/containers/proxyTester/state | jq .pid) -- nc -6 -l 1337 > proxyTest.out &
  sleep 2

  echo ${MESSAGE} | nc 127.0.0.1 ${LOCAL_TCP_PORT} &
  sleep 1

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f proxyTest.out
  lxc stop proxyTester
  lxc delete proxyTester
}

