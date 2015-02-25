test_basic_usage() {
  if ! lxc image alias list | grep -q ^testimage$; then
    if [ -e "$LXD_TEST_IMAGE" ]; then
        IMAGE_SHA256=$(sha256sum "$LXD_TEST_IMAGE" | cut -d ' ' -f1)
        lxc image import $LXD_TEST_IMAGE
        lxc image alias create testimage $IMAGE_SHA256
    else
        ../scripts/lxd-images import lxc ubuntu trusty amd64 --alias testimage
    fi
  fi

  lxc launch testimage foo
  # should fail if foo isn't running
  lxc stop foo
  lxc delete foo

  lxc init testimage foo

  # did it get created?
  lxc list | grep foo

  # cycle it a few times
  lxc start foo
  lxc stop foo
  lxc start foo

  # Make sure it is the right version
  lxc exec foo /bin/cat /etc/issue | grep 14.04
  echo foo | lxc exec foo tee /tmp/foo

  # This is why we can't have nice things.
  content=$(cat "${LXD_DIR}/lxc/foo/rootfs/tmp/foo")
  [ "$content" = "foo" ]

  # cleanup
  lxc stop foo
  lxc delete foo

  # now, make sure create type 'none' works
  mkdir -p "${LXD_DIR}/lxc/nonetype"
  wait_for my_curl -X POST $BASEURL/1.0/containers -d '{"name": "nonetype", "source": {"type": "none"}}'
  rm -rf "${LXD_DIR}/lxc/nonetype"
}
