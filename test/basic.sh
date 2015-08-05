gen_third_cert() {
	[ -f $LXD_CONF/client3.crt ] && return
	mv $LXD_CONF/client.crt $LXD_CONF/client.crt.bak
	mv $LXD_CONF/client.key $LXD_CONF/client.key.bak
	lxc list > /dev/null 2>&1
	mv $LXD_CONF/client.crt $LXD_CONF/client3.crt
	mv $LXD_CONF/client.key $LXD_CONF/client3.key
	mv $LXD_CONF/client.crt.bak $LXD_CONF/client.crt
	mv $LXD_CONF/client.key.bak $LXD_CONF/client.key
}

test_basic_usage() {

  ensure_import_testimage

  lxc remote set-default DEFAULT

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
  lxc list | grep -v foo
  lxc list | grep bar

  # Test container copy
  lxc copy bar foo
  lxc delete foo

  # gen untrusted cert
  gen_third_cert

  # Test container publish
  lxc publish bar --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  curl -k -s --cert $LXD_CONF/client3.crt --key $LXD_CONF/client3.key -X GET $BASEURL/1.0/images | grep "/1.0/images/" && false
  lxc image delete foo-image

  # Test public images
  lxc publish --public bar --alias=foo-image2
  curl -k -s --cert $LXD_CONF/client3.crt --key $LXD_CONF/client3.key -X GET $BASEURL/1.0/images | grep "/1.0/images/"
  lxc image delete foo-image2

  # Test snapshot publish
  lxc snapshot bar
  lxc publish bar/snap0 --alias foo
  lxc init foo bar2
  lxc list | grep bar2
  lxc delete bar2
  lxc image delete foo

  # Delete the bar container we've used for several tests
  lxc delete bar
	# lxc delete should also delete all snapshots of bar
	[ ! -d ${LXD_DIR}/snapshots/bar ]

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
  lxc stop foo --force # stop is hanging
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
  content=$(cat "${LXD_DIR}/containers/foo/rootfs/tmp/foo")
  [ "$content" = "foo" ]

  # cleanup
  lxc delete foo

  # make sure that privileged containers are not world-readable
  lxc profile create unconfined
  lxc profile set unconfined security.privileged true
  lxc init testimage foo2 -p unconfined
  [ `stat -c "%a" ${LXD_DIR}/containers/foo2` = 700 ]
  lxc delete foo2
  lxc profile delete unconfined

  # Ephemeral
  lxc launch testimage foo -e
  lxc exec foo reboot
  sleep 2
  lxc stop foo --force
  sleep 2
  ! lxc list | grep -q foo
}
