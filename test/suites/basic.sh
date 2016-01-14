#!/bin/sh

gen_third_cert() {
  [ -f "${LXD_CONF}/client3.crt" ] && return
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  lxc_remote list > /dev/null 2>&1
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client3.crt"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client3.key"
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"
}

test_basic_usage() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Test image export
  sum=$(lxc image info testimage | grep ^Fingerprint | cut -d' ' -f2)
  lxc image export testimage "${LXD_DIR}/"
  if [ -e "${LXD_TEST_IMAGE:-}" ]; then
    name=$(basename "${LXD_TEST_IMAGE}")
  else
    name=${sum}.tar.xz
  fi
  [ "${sum}" = "$(sha256sum "${LXD_DIR}/${name}" | cut -d' ' -f1)" ]

  # Test image delete
  lxc image delete testimage

  # test GET /1.0, since the client always puts to /1.0/
  my_curl -f -X GET "https://${LXD_ADDR}/1.0"
  my_curl -f -X GET "https://${LXD_ADDR}/1.0/containers"

  # Re-import the image
  mv "${LXD_DIR}/${name}" "${LXD_DIR}/testimage.tar.xz"
  lxc image import "${LXD_DIR}/testimage.tar.xz" --alias testimage
  rm "${LXD_DIR}/testimage.tar.xz"

  # Test filename for image export (should be "out")
  lxc image export testimage "${LXD_DIR}/"
  [ "${sum}" = "$(sha256sum "${LXD_DIR}/testimage.tar.xz" | cut -d' ' -f1)" ]
  rm "${LXD_DIR}/testimage.tar.xz"

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

  # don't allow requests without a cert to get trusted data
  curl -k -s -X GET "https://${LXD_ADDR}/1.0/containers/foo" | grep 403

  # Test unprivileged container publish
  lxc publish bar --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  curl -k -s --cert "${LXD_CONF}/client3.crt" --key "${LXD_CONF}/client3.key" -X GET "https://${LXD_ADDR}/1.0/images" | grep "/1.0/images/" && false
  lxc image delete foo-image

  # Test privileged container publish
  lxc profile create priv
  lxc profile set priv security.privileged true
  lxc init testimage barpriv -p default -p priv
  lxc publish barpriv --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  curl -k -s --cert "${LXD_CONF}/client3.crt" --key "${LXD_CONF}/client3.key" -X GET "https://${LXD_ADDR}/1.0/images" | grep "/1.0/images/" && false
  lxc image delete foo-image
  lxc delete barpriv
  lxc profile delete priv

  # Test that containers without metadata.yaml are published successfully.
  # Note that this quick hack won't work for LVM, since it doesn't always mount
  # the container's filesystem. That's ok though: the logic we're trying to
  # test here is independent of storage backend, so running it for just one
  # backend (or all non-lvm backends) is enough.
  if [ "${LXD_BACKEND}" != "lvm" ]; then
    lxc init testimage nometadata
    rm "${LXD_DIR}/containers/nometadata/metadata.yaml"
    lxc publish nometadata --alias=nometadata-image
    lxc image delete nometadata-image
    lxc delete nometadata
  fi

  # Test public images
  lxc publish --public bar --alias=foo-image2
  curl -k -s --cert "${LXD_CONF}/client3.crt" --key "${LXD_CONF}/client3.key" -X GET "https://${LXD_ADDR}/1.0/images" | grep "/1.0/images/"
  lxc image delete foo-image2

  # Test invalid container names
  ! lxc init testimage -abc
  ! lxc init testimage abc-
  ! lxc init testimage 1234
  ! lxc init testimage 12test
  ! lxc init testimage a_b_c
  ! lxc init testimage aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

  # Test snapshot publish
  lxc snapshot bar
  lxc publish bar/snap0 --alias foo
  lxc init foo bar2
  lxc list | grep bar2
  lxc delete bar2
  lxc image delete foo

  # test basic alias support
  printf "aliases:\n  ls: list" >> "${LXD_CONF}/config.yml"
  lxc ls

  # Delete the bar container we've used for several tests
  lxc delete bar

  # lxc delete should also delete all snapshots of bar
  [ ! -d "${LXD_DIR}/snapshots/bar" ]

  # Test randomly named container creation
  lxc init testimage
  RDNAME=$(lxc list | tail -n2 | grep ^\| | awk '{print $2}')
  lxc delete "${RDNAME}"

  # Test "nonetype" container creation
  wait_for "${LXD_ADDR}" my_curl -X POST "https://${LXD_ADDR}/1.0/containers" \
        -d "{\"name\":\"nonetype\",\"source\":{\"type\":\"none\"}}"
  lxc delete nonetype

  # Test "nonetype" container creation with an LXC config
  wait_for "${LXD_ADDR}" my_curl -X POST "https://${LXD_ADDR}/1.0/containers" \
        -d "{\"name\":\"configtest\",\"config\":{\"raw.lxc\":\"lxc.hook.clone=/bin/true\"},\"source\":{\"type\":\"none\"}}"
  [ "$(my_curl "https://${LXD_ADDR}/1.0/containers/configtest" | jq -r .metadata.config[\"raw.lxc\"])" = "lxc.hook.clone=/bin/true" ]
  lxc delete configtest

  # Test socket activation
  LXD_ACTIVATION_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_ACTIVATION_DIR}"
  (
    set -e
    # shellcheck disable=SC2030
    LXD_DIR=${LXD_ACTIVATION_DIR}
    ensure_import_testimage
    lxd activateifneeded --debug 2>&1 | grep -q "Daemon has core.https_address set, activating..."
    lxc config unset core.https_address --force-local
    lxd activateifneeded --debug 2>&1 | grep -q -v "activating..."
    lxc init testimage autostart --force-local
    lxd activateifneeded --debug 2>&1 | grep -q -v "activating..."
    lxc config set autostart boot.autostart true --force-local
    lxd activateifneeded --debug 2>&1 | grep -q "Daemon has auto-started containers, activating..."
    lxc delete autostart --force-local
  )
  # shellcheck disable=SC2031
  LXD_DIR=${LXD_DIR}
  kill_lxd "${LXD_ACTIVATION_DIR}"

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

  if [ -n "${mac1}" ] && [ -n "${mac2}" ] && [ "${mac1}" != "${mac2}" ]; then
    echo "==> MAC addresses didn't match across restarts (${mac1} vs ${mac2})"
    false
  fi

  # check that we can set the environment
  lxc exec foo pwd | grep /root
  lxc exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  lxc exec foo ip link show | grep eth0

  # test file transfer
  echo abc > "${LXD_DIR}/in"

  lxc file push "${LXD_DIR}/in" foo/root/
  lxc exec foo /bin/cat /root/in | grep abc
  lxc exec foo -- /bin/rm -f root/in

  lxc file push "${LXD_DIR}/in" foo/root/in1
  lxc exec foo /bin/cat /root/in1 | grep abc
  lxc exec foo -- /bin/rm -f root/in1

  # make sure stdin is chowned to our container root uid (Issue #590)
  [ -t 0 ] && lxc exec foo -- chown 1000:1000 /proc/self/fd/0

  echo foo | lxc exec foo tee /tmp/foo

  # Detect regressions/hangs in exec
  sum=$(ps aux | tee "${LXD_DIR}/out" | lxc exec foo md5sum | cut -d' ' -f1)
  [ "${sum}" = "$(md5sum "${LXD_DIR}/out" | cut -d' ' -f1)" ]
  rm "${LXD_DIR}/out"

  # FIXME: make this backend agnostic
  if [ "${LXD_BACKEND}" = "dir" ]; then
    content=$(cat "${LXD_DIR}/containers/foo/rootfs/tmp/foo")
    [ "${content}" = "foo" ]
  fi

  lxc launch testimage deleterunning
  my_curl -X DELETE "https://${LXD_ADDR}/1.0/containers/deleterunning" | grep "container is running"
  lxc delete deleterunning

  # cleanup
  lxc delete foo

  # check that an apparmor profile is created for this container, that it is
  # unloaded on stop, and that it is deleted when the container is deleted
  lxc launch testimage lxd-apparmor-test
  aa-status | grep "lxd-lxd-apparmor-test_<${LXD_DIR}>"
  lxc stop lxd-apparmor-test --force
  ! aa-status | grep -q "lxd-lxd-apparmor-test_<${LXD_DIR}>"
  lxc delete lxd-apparmor-test
  [ ! -f "${LXD_DIR}/security/apparmor/profiles/lxd-lxd-apparmor-test" ]

  # make sure that privileged containers are not world-readable
  lxc profile create unconfined
  lxc profile set unconfined security.privileged true
  lxc init testimage foo2 -p unconfined
  [ "$(stat -L -c "%a" "${LXD_DIR}/containers/foo2")" = "700" ]
  lxc delete foo2
  lxc profile delete unconfined

  # Ephemeral
  lxc launch testimage foo -e

  OLD_INIT=$(lxc info foo | grep ^Init)
  lxc exec foo reboot

  # shellcheck disable=SC2034
  for i in $(seq 10); do
    NEW_INIT=$(lxc info foo | grep ^Init || true)

    if [ -n "${NEW_INIT}" ] && [ "${OLD_INIT}" != "${NEW_INIT}" ]; then
      break
    fi

    sleep 0.5
  done

  lxc stop foo --force
  ! lxc list | grep -q foo
}
