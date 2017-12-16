test_proxy_device() {
  
  MESSAGE="Proxy device test string"

  lxc launch busybox proxyTester
  lxc config device add proxyTester proxyDev proxy listen=tcp:127.0.0.1:1234 connect=tcp:127.0.0.1:4321 bind=host
  if [ $(lxc config device list proxyTester) != "proxyDev" ]; then
    echo "Proxy device was not added to container"
  fi


  lxc config device remove proxyTester proxyDev
  if [ $(lxc config device list proxyTester) ]; then 
    echo "Proxy device was not removed from container"
  fi

  lxc config device add proxyTester proxyDev proxy listen=tcp:127.0.0.1:1234 connect=tcp:127.0.0.1:4321 bind=host
  lxc stop proxyTester
  if [ $(lxc config device list proxyTester) != "proxyDev" ]; then
    echo "Proxy device should not be deleted from config on container stop"
  fi
  lxc delete proxyTester

  lxc launch busybox proxyTester
  if [ $(lxc config device list proxyTester) ]; then 
    echo "Proxy device was not deleted from config on container deletion"
  fi


  lxc config device add proxyTester proxyDev proxy listen=tcp:127.0.0.1:1234 connect=tcp:127.0.0.1:4321 bind=host
  nsenter -n -t $(lxc query /1.0/containers/proxyTester/state | jq .pid) -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  exec 3>/dev/tcp/localhost/1234
  echo ${MESSAGE} >&3
  sleep 1

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly send data from host to container"
  fi

  rm -f proxyTest.out

  lxc restart proxyTester
  if [ $(lxc config device list proxyTester) != "proxyDev" ]; then
    echo "Proxy device should not be removed on container restart"
  fi

  nsenter -n -t $(lxc query /1.0/containers/proxyTester/state | jq .pid) -- nc -6 -l 4321 > proxyTest.out &
  sleep 2

  exec 3>/dev/tcp/localhost/1234
  echo ${MESSAGE} >&3
  sleep 1

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly restart on container restart"
  fi

  rm -f proxyTest.out

  lxc config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  nsenter -n -t $(lxc query /1.0/containers/proxyTester/state | jq .pid) -- nc -6 -l 1337 > proxyTest.out &
  sleep 2

  exec 3>/dev/tcp/localhost/1234
  echo ${MESSAGE} >&3
  sleep 1

  if [ "$(cat proxyTest.out)" != "${MESSAGE}" ]; then
    echo "Proxy device did not properly restart when config was updated"
  fi

  rm -f proxyTest.out
  lxc stop proxyTester
  lxc delete proxyTester
}

