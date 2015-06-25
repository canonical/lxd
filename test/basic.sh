test_basic_usage() {
  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
    if [ -e "$LXD_TEST_IMAGE" ]; then
        lxc image import $LXD_TEST_IMAGE --alias testimage
    else
        ../scripts/lxd-images import busybox --alias testimage
    fi
  fi

  # Test image export
  sum=$(lxc image info testimage | grep ^Fingerprint | cut -d' ' -f2)
  lxc image export testimage ${LXD_DIR}/
  if [ -e "$LXD_TEST_IMAGE" ]; then
      name=$(basename $LXD_TEST_IMAGE)
  else
      name=${sum}.tar.xz
  fi
  [ "$sum" = "$(sha256sum ${LXD_DIR}/${name} | cut -d' ' -f1)" ]

  # Test image delete
  lxc image delete testimage

  # Re-import the image
  mv ${LXD_DIR}/$name ${LXD_DIR}/testimage.tar.xz
  lxc image import ${LXD_DIR}/testimage.tar.xz --alias testimage
  rm ${LXD_DIR}/testimage.tar.xz

  # Test filename for image export (should be "out")
  lxc image export testimage ${LXD_DIR}/
  [ "$sum" = "$(sha256sum ${LXD_DIR}/testimage.tar.xz | cut -d' ' -f1)" ]
  rm ${LXD_DIR}/testimage.tar.xz

  # Test container creation
  lxc init testimage foo
  lxc list | grep foo | grep STOPPED
  lxc list fo | grep foo | grep STOPPED

  # Test container rename
  lxc move foo bar

  # Test container copy
  lxc copy bar foo
  lxc delete foo

  # Test container publish
  lxc publish bar --alias=foo prop1=val1
  lxc image show foo | grep val1
  lxc image delete foo
  lxc delete bar

  # Test randomly named container creation
  lxc init testimage
  RDNAME=$(lxc list | grep STOPPED | cut -d' ' -f2)
  lxc delete $RDNAME

  # Test "nonetype" container creation
  wait_for my_curl -X POST $BASEURL/1.0/containers \
        -d "{\"name\":\"nonetype\",\"source\":{\"type\":\"none\"}}"
  lxc delete nonetype

  # Test "nonetype" container creation with an LXC config
  wait_for my_curl -X POST $BASEURL/1.0/containers \
        -d "{\"name\":\"configtest\",\"config\":{\"raw.lxc\":\"lxc.hook.clone=/bin/true\"},\"source\":{\"type\":\"none\"}}"
  [ "$(my_curl $BASEURL/1.0/containers/configtest | jq -r .metadata.config[\"raw.lxc\"])" = "lxc.hook.clone=/bin/true" ]
  lxc delete configtest

  # Anything below this will not get run inside Travis-CI
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  # Create and start a container
  lxc launch testimage foo
  lxc list | grep foo | grep RUNNING
  lxc stop foo --force  # stop is hanging

  # cycle it a few times
  lxc start foo
  mac1=$(lxc exec foo cat /sys/class/net/eth0/address)
  lxc stop foo  --force # stop is hanging
  lxc start foo
  mac2=$(lxc exec foo cat /sys/class/net/eth0/address)

  if [ "$mac1" != "$mac2" ]; then
    echo "==> MAC addresses didn't match across restarts"
    false
  fi

  # check that we can set the environment
  lxc exec foo pwd | grep /root
  lxc exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  lxc exec foo ip link show | grep eth0

  # test file transfer
  echo abc > ${LXD_DIR}/in

  lxc file push ${LXD_DIR}/in foo/root/
  lxc exec foo /bin/cat /root/in | grep abc
  lxc exec foo -- /bin/rm -f root/in

  lxc file push ${LXD_DIR}/in foo/root/in1
  lxc exec foo /bin/cat /root/in1 | grep abc
  lxc exec foo -- /bin/rm -f root/in1

  # make sure stdin is chowned to our container root uid (Issue #590)
  lxc exec foo -- chown 1000:1000 /proc/self/fd/0

  echo foo | lxc exec foo tee /tmp/foo

  # Detect regressions/hangs in exec
  sum=$(ps aux | tee ${LXD_DIR}/out | lxc exec foo md5sum | cut -d' ' -f1)
  [ "$sum" = "$(md5sum ${LXD_DIR}/out | cut -d' ' -f1)" ]
  rm ${LXD_DIR}/out

  # This is why we can't have nice things.
  content=$(cat "${LXD_DIR}/lxc/foo/rootfs/tmp/foo")
  [ "$content" = "foo" ]

  # cleanup
  lxc delete foo

  # Ephemeral
  lxc launch testimage foo -e
  lxc exec foo reboot
  sleep 2
  lxc stop foo --force
  sleep 2
  ! lxc list | grep -q foo
}
