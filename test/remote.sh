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

test_remote_admin() {
  bad=0
  (echo y;  sleep 3;  echo bad) | lxc remote add badpass 127.0.0.1:18443 $debug || true
  lxc list badpass && bad=1 || true
  if [ "$bad" -eq 1 ]; then
      echo "bad password accepted" && false
  fi

  (echo y;  sleep 3;  echo foo) |  lxc remote add local 127.0.0.1:18443 $debug
  lxc remote list | grep 'local'

  lxc remote set-default local
  [ "$(lxc remote get-default)" = "local" ]

  lxc remote rename local foo
  lxc remote list | grep 'foo'
  lxc remote list | grep -v 'local'
  [ "$(lxc remote get-default)" = "foo" ]

  lxc remote remove foo
  [ "$(lxc remote get-default)" = "" ]

  # This is a test for #91, we expect this to hang asking for a password if we
  # tried to re-add our cert.
  echo y | lxc remote add local 127.0.0.1:18443 $debug

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_second_cert
  lxc config trust add "$LXD_CONF/client2.crt"
  lxc config trust list | grep client2
  lxc config trust remove client2
}

test_remote_usage() {
  (echo y;  sleep 3;  echo foo) |  lxc remote add lxd2 127.0.0.1:18444 $debug

  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  # we need a public image on local
  lxc image export local:testimage ${LXD_DIR}/foo.img
  lxc image delete local:testimage
  sum=`sha256sum ${LXD_DIR}/foo.img`
  lxc image import ${LXD_DIR}/foo.img local: --public
  lxc image alias create local:testimage $sum

  # launch testimage stored on local as container c1 on lxd2
  lxc launch local:testimage lxd2:c1

  # make sure it is running
  lxc list lxd2: | grep c1 | grep RUNNING

  lxc stop lxd2:c1 --force

  lxc delete lxd2:c1
}
