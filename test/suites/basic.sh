#!/bin/sh

test_basic_usage() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Test image export
  sum=$(lxc image info testimage | grep ^Fingerprint | cut -d' ' -f2)
  lxc image export testimage "${LXD_DIR}/"
  [ "${sum}" = "$(sha256sum "${LXD_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]

  # Test an alias with slashes
  lxc image show "${sum}"
  lxc image alias create a/b/ "${sum}"
  lxc image alias delete a/b/

  # Test alias list filtering
  lxc image alias create foo "${sum}"
  lxc image alias create bar "${sum}"
  lxc image alias list local: | grep -q foo
  lxc image alias list local: | grep -q bar
  lxc image alias list local: foo | grep -q -v bar
  lxc image alias list local: "${sum}" | grep -q foo
  lxc image alias list local: non-existent | grep -q -v non-existent
  lxc image alias delete foo
  lxc image alias delete bar

  # Test image list output formats (table & json)
  lxc image list --format table | grep -q testimage
  lxc image list --format json \
    | jq '.[]|select(.alias[0].name="testimage")' \
    | grep -q '"name": "testimage"'

  # Test image delete
  lxc image delete testimage

  # test GET /1.0, since the client always puts to /1.0/
  my_curl -f -X GET "https://${LXD_ADDR}/1.0"
  my_curl -f -X GET "https://${LXD_ADDR}/1.0/containers"

  # Re-import the image
  mv "${LXD_DIR}/${sum}.tar.xz" "${LXD_DIR}/testimage.tar.xz"
  lxc image import "${LXD_DIR}/testimage.tar.xz" --alias testimage
  rm "${LXD_DIR}/testimage.tar.xz"

  # Test filename for image export
  lxc image export testimage "${LXD_DIR}/"
  [ "${sum}" = "$(sha256sum "${LXD_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]
  rm "${LXD_DIR}/${sum}.tar.xz"

  # Test custom filename for image export
  lxc image export testimage "${LXD_DIR}/foo"
  [ "${sum}" = "$(sha256sum "${LXD_DIR}/foo.tar.xz" | cut -d' ' -f1)" ]
  rm "${LXD_DIR}/foo.tar.xz"


  # Test image export with a split image.
  deps/import-busybox --split --alias splitimage

  sum=$(lxc image info splitimage | grep ^Fingerprint | cut -d' ' -f2)

  lxc image export splitimage "${LXD_DIR}"
  [ "${sum}" = "$(cat "${LXD_DIR}/meta-${sum}.tar.xz" "${LXD_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]
  
  # Delete the split image and exported files
  rm "${LXD_DIR}/${sum}.tar.xz"
  rm "${LXD_DIR}/meta-${sum}.tar.xz"
  lxc image delete splitimage

  # Redo the split image export test, this time with the --filename flag
  # to tell import-busybox to set the 'busybox' filename in the upload.
  # The sum should remain the same as its the same image.
  deps/import-busybox --split --filename --alias splitimage

  lxc image export splitimage "${LXD_DIR}"
  [ "${sum}" = "$(cat "${LXD_DIR}/meta-${sum}.tar.xz" "${LXD_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]
  
  # Delete the split image and exported files
  rm "${LXD_DIR}/${sum}.tar.xz"
  rm "${LXD_DIR}/meta-${sum}.tar.xz"
  lxc image delete splitimage


  # Test container creation
  lxc init testimage foo
  lxc list | grep foo | grep STOPPED
  lxc list fo | grep foo | grep STOPPED

  # Test list json format
  lxc list --format json | jq '.[]|select(.name="foo")' | grep '"name": "foo"'

  # Test container rename
  lxc move foo bar
  lxc list | grep -v foo
  lxc list | grep bar

  # Test container copy
  lxc copy bar foo
  lxc delete foo

  # gen untrusted cert
  gen_cert client3

  # don't allow requests without a cert to get trusted data
  curl -k -s -X GET "https://${LXD_ADDR}/1.0/containers/foo" | grep 403

  # Test unprivileged container publish
  lxc publish bar --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  curl -k -s --cert "${LXD_CONF}/client3.crt" --key "${LXD_CONF}/client3.key" -X GET "https://${LXD_ADDR}/1.0/images" | grep "/1.0/images/" && false
  lxc image delete foo-image

# Test image compression on publish
  lxc publish bar --alias=foo-image-compressed --compression=bzip2 prop=val1
  lxc image show foo-image-compressed | grep val1
  curl -k -s --cert "${LXD_CONF}/client3.crt" --key "${LXD_CONF}/client3.key" -X GET "https://${LXD_ADDR}/1.0/images" | grep "/1.0/images/" && false
  lxc image delete foo-image-compressed


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

  # Test alias support
  cp "${LXD_CONF}/config.yml" "${LXD_CONF}/config.yml.bak"

  #   1. Basic built-in alias functionality
  [ "$(lxc ls)" = "$(lxc list)" ]
  #   2. Basic user-defined alias functionality
  printf "aliases:\n  l: list\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc l)" = "$(lxc list)" ]
  #   3. Built-in aliases and user-defined aliases can coexist
  [ "$(lxc ls)" = "$(lxc l)" ]
  #   4. Multi-argument alias keys and values
  printf "  i ls: image list\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc i ls)" = "$(lxc image list)" ]
  #   5. Aliases where len(keys) != len(values) (expansion/contraction of number of arguments)
  printf "  ils: image list\n  container ls: list\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc ils)" = "$(lxc image list)" ]
  [ "$(lxc container ls)" = "$(lxc list)" ]
  #   6. User-defined aliases override built-in aliases
  printf "  cp: list\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc ls)" = "$(lxc cp)" ]
  #   7. User-defined aliases override commands and don't recurse
  lxc init testimage foo
  LXC_CONFIG_SHOW=$(lxc config show foo --expanded)
  printf "  config show: config show --expanded\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc config show foo)" = "$LXC_CONFIG_SHOW" ]
  lxc delete foo

  # Restore the config to remove the aliases
  mv "${LXD_CONF}/config.yml.bak" "${LXD_CONF}/config.yml"

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
  # shellcheck disable=SC2102
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

  # check that we can put files in nonexistent directories in stopped
  # containers
  lxc file push /etc/hosts foo/mkdir/p/this/dir/hosts
  lxc file pull foo/mkdir/p/this/dir/hosts "$TEST_DIR"/hosts
  diff "$TEST_DIR"/hosts /etc/hosts

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

  # Test last_used_at field is working properly
  lxc init testimage last-used-at-test
  lxc list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep '1970-01-01T00:00:00Z'
  lxc start last-used-at-test
  lxc list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep -v '1970-01-01T00:00:00Z'

  # check that we can set the environment
  lxc exec foo pwd | grep /root
  lxc exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  lxc exec foo ip link show | grep eth0

  # check that we can get the return code for a non- wait-for-websocket exec
  op=$(my_curl -X POST "https://${LXD_ADDR}/1.0/containers/foo/exec" -d '{"command": ["sleep", "1"], "environment": {}, "wait-for-websocket": false, "interactive": false}' | jq -r .operation)
  [ "$(my_curl "https://${LXD_ADDR}${op}/wait" | jq -r .metadata.metadata.return)" != "null" ]

  # test file transfer
  echo abc > "${LXD_DIR}/in"

  lxc file push "${LXD_DIR}/in" foo/root/
  lxc exec foo /bin/cat /root/in | grep abc
  lxc exec foo -- /bin/rm -f root/in

  lxc file push "${LXD_DIR}/in" foo/root/in1
  lxc exec foo /bin/cat /root/in1 | grep abc
  lxc exec foo -- /bin/rm -f root/in1

  # test lxc file edit doesn't change target file's owner and permissions
  echo "content" | lxc file push - foo/tmp/edit_test
  lxc exec foo -- chown 55.55 /tmp/edit_test
  lxc exec foo -- chmod 555 /tmp/edit_test
  echo "new content" | lxc file edit foo/tmp/edit_test
  [ "$(lxc exec foo -- cat /tmp/edit_test)" = "new content" ]
  [ "$(lxc exec foo -- stat -c \"%u %g %a\" /tmp/edit_test)" = "55 55 555" ]

  # make sure stdin is chowned to our container root uid (Issue #590)
  [ -t 0 ] && [ -t 1 ] && lxc exec foo -- chown 1000:1000 /proc/self/fd/0

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
  lxc delete deleterunning -f

  # cleanup
  lxc delete foo -f

  # check that an apparmor profile is created for this container, that it is
  # unloaded on stop, and that it is deleted when the container is deleted
  lxc launch testimage lxd-apparmor-test
  aa-status | grep "lxd-lxd-apparmor-test_<${LXD_DIR}>"
  lxc stop lxd-apparmor-test --force
  ! aa-status | grep -q "lxd-lxd-apparmor-test_<${LXD_DIR}>"
  lxc delete lxd-apparmor-test
  [ ! -f "${LXD_DIR}/security/apparmor/profiles/lxd-lxd-apparmor-test" ]

  lxc launch testimage lxd-seccomp-test
  init=$(lxc info lxd-seccomp-test | grep Pid | cut -f2 -d" ")
  [ "$(grep Seccomp "/proc/${init}/status" | cut -f2)" -eq "2" ]
  lxc stop --force lxd-seccomp-test
  lxc config set lxd-seccomp-test security.syscalls.blacklist_default false
  lxc start lxd-seccomp-test
  init=$(lxc info lxd-seccomp-test | grep Pid | cut -f2 -d" ")
  [ "$(grep Seccomp "/proc/${init}/status" | cut -f2)" -eq "0" ]
  lxc stop --force lxd-seccomp-test

  # make sure that privileged containers are not world-readable
  lxc profile create unconfined
  lxc profile set unconfined security.privileged true
  lxc init testimage foo2 -p unconfined
  [ "$(stat -L -c "%a" "${LXD_DIR}/containers/foo2")" = "700" ]
  lxc delete foo2
  lxc profile delete unconfined

  # Test boot.host_shutdown_timeout config setting
  lxc init testimage configtest --config boot.host_shutdown_timeout=45
  [ "$(lxc config get configtest boot.host_shutdown_timeout)" -eq 45 ]
  lxc config set configtest boot.host_shutdown_timeout 15
  [ "$(lxc config get configtest boot.host_shutdown_timeout)" -eq 15 ]
  lxc delete configtest

  # Test deleting multiple images
  # Start 3 containers to create 3 different images
  lxc launch testimage c1
  lxc launch testimage c2
  lxc launch testimage c3
  lxc exec c1 -- touch /tmp/c1
  lxc exec c2 -- touch /tmp/c2
  lxc exec c3 -- touch /tmp/c3
  lxc publish --force c1 --alias=image1
  lxc publish --force c2 --alias=image2
  lxc publish --force c3 --alias=image3
  # Delete multiple images with lxc delete and confirm they're deleted
  lxc image delete local:image1 local:image2 local:image3
  ! lxc image list | grep -q image1
  ! lxc image list | grep -q image2
  ! lxc image list | grep -q image3
  # Cleanup the containers
  lxc delete --force c1 c2 c3

  # Ephemeral
  lxc launch testimage foo -e

  OLD_INIT=$(lxc info foo | grep ^Pid)
  lxc exec foo reboot

  REBOOTED="false"

  # shellcheck disable=SC2034
  for i in $(seq 10); do
    NEW_INIT=$(lxc info foo | grep ^Pid || true)

    if [ -n "${NEW_INIT}" ] && [ "${OLD_INIT}" != "${NEW_INIT}" ]; then
      REBOOTED="true"
      break
    fi

    sleep 0.5
  done

  [ "${REBOOTED}" = "true" ]

  # Workaround for LXC bug which causes LXD to double-start containers
  # on reboot
  sleep 2

  lxc stop foo --force || true
  ! lxc list | grep -q foo
}
