test_basic_usage() {
  if ! lxc image alias list | grep -q ^testimage$; then
    if [ -e "$LXD_TEST_IMAGE" ]; then
        IMAGE_SHA256=$(sha256sum "$LXD_TEST_IMAGE" | cut -d ' ' -f1)
        lxc image import $LXD_TEST_IMAGE
        lxc image alias create testimage $IMAGE_SHA256
    else
        ../scripts/lxd-images import busybox --alias testimage
    fi
  fi

  # Test image export
  sum=$(lxc image info testimage | grep ^Hash | cut -d' ' -f2)
  lxc image export testimage ${LXD_DIR}/out
  [ "$sum" = "$(sha256sum ${LXD_DIR}/out | cut -d' ' -f1)" ]

  # Test iamge delete
  lxc image delete testimage

  # Re-import the image
  lxc image import ${LXD_DIR}/out
  lxc image alias create testimage $sum
  rm ${LXD_DIR}/out

  # Test filename for image export (should be "out")
  lxc image export testimage ${LXD_DIR}/
  [ "$sum" = "$(sha256sum ${LXD_DIR}/out | cut -d' ' -f1)" ]
  rm ${LXD_DIR}/out

  # Test container creation
  lxc init testimage foo
  lxc list | grep foo | grep STOPPED
  lxc delete foo

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
  lxc stop foo  --force # stop is hanging
  lxc start foo

  # check that we can set the environment
  lxc exec foo pwd | grep /root
  lxc exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  lxc exec foo ip link show | grep eth0

  # Make sure it is the right version
  echo abc > ${LXD_DIR}/in
  lxc file push ${LXD_DIR}/in foo/root/
  lxc exec foo /bin/cat /root/in | grep abc
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
}
