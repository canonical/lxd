test_remote() {
  bad=0
  (echo y;  sleep 3;  echo bad) | lxc remote add badpass 127.0.0.1:8443 --debug || true
  lxc list badpass && bad=1 || true
  if [ "$bad" -eq 1 ]; then
      echo "bad password accepted" && false
  fi

  (echo y;  sleep 3;  echo foo) |  lxc remote add local 127.0.0.1:8443 --debug
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
  echo y | lxc remote add local 127.0.0.1:8443 --debug

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  lxc config trust add "$LXD_CONF/client.crt"
  lxc config trust list | grep client
  lxc config trust remove client
}
