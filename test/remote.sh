gen_second_cert() {
	[ -f $LXD_CONF/client2.crt ] && return
	mv $LXD_CONF/client.crt $LXD_CONF/client.crt.bak
	mv $LXD_CONF/client.key $LXD_CONF/client.key.bak
	lxc list > /dev/null 2>&1
	mv $LXD_CONF/client.crt $LXD_CONF/client2.crt
	mv $LXD_CONF/client.key $LXD_CONF/client2.key
	mv $LXD_CONF/client.crt.bak $LXD_CONF/client.crt
	mv $LXD_CONF/client.key.bak $LXD_CONF/client.key
}

test_remote_url() {
  for url in localhost:18443 https://localhost:18443; do
    (echo y;  sleep 3;  echo foo) | lxc remote add test $url
    lxc finger test:
    lxc config trust remove localhost
    lxc remote remove test
  done

  for url in images.linuxcontainers.org https://images.linuxcontainers.org ${LXD_DIR}/unix.socket unix:${LXD_DIR}/unix.socket unix://${LXD_DIR}/unix.socket; do
    lxc remote add test $url
    lxc finger test:
    lxc remote remove test
  done
}

test_remote_admin() {
  bad=0
  (echo y;  sleep 3;  echo bad) | lxc remote add badpass 127.0.0.1:18443 $debug || true
  lxc list badpass: && bad=1 || true
  if [ "$bad" -eq 1 ]; then
      echo "bad password accepted" && false
  fi

  (echo y;  sleep 3;  echo foo) |  lxc remote add localhost 127.0.0.1:18443 $debug
  lxc remote list | grep 'localhost'

  lxc remote set-default localhost
  [ "$(lxc remote get-default)" = "localhost" ]

  lxc remote rename localhost foo
  lxc remote list | grep 'foo'
  lxc remote list | grep -v 'localhost'
  [ "$(lxc remote get-default)" = "foo" ]

  lxc remote remove foo
  [ "$(lxc remote get-default)" = "" ]

  # This is a test for #91, we expect this to hang asking for a password if we
  # tried to re-add our cert.
  echo y | lxc remote add localhost 127.0.0.1:18443 $debug

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_second_cert
  lxc config trust add "$LXD_CONF/client2.crt"
  lxc config trust list | grep client2
  lxc config trust remove client2

  # Check that we can add domains with valid certs without confirmation:
  lxc remote add images images.linuxcontainers.org
  lxc remote add images2 images.linuxcontainers.org:443
}

test_remote_usage() {
  (echo y;  sleep 3;  echo foo) |  lxc remote add lxd2 127.0.0.1:18444 $debug

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  # we need a public image on localhost
  lxc image export localhost:testimage ${LXD_DIR}/foo.img
  lxc image delete localhost:testimage
  sum=`sha256sum ${LXD_DIR}/foo.img`
  lxc image import ${LXD_DIR}/foo.img localhost: --public
  lxc image alias create localhost:testimage $sum

  # launch testimage stored on localhost as container c1 on lxd2
  lxc launch localhost:testimage lxd2:c1

  # make sure it is running
  lxc list lxd2: | grep c1 | grep RUNNING

  lxc stop lxd2:c1 --force

  lxc delete lxd2:c1
}
