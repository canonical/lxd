test_basic_usage() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Test image export
  sum="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
  lxc image export testimage "${LXD_DIR}/"
  [ "${sum}" = "$(sha256sum "${LXD_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]

  # Test an alias with slashes
  lxc image show "${sum}"
  lxc image alias create a/b/ "${sum}"

  echo "Test using alias with slashes"
  lxc init a/b/ c1 -d "${SMALL_ROOT_DISK}"
  lxc delete c1

  # Ensure aliased image won't launch with vm flag set
  ! lxc launch a/b/ --vm || false

  lxc image alias delete a/b/

  # Test alias list filtering
  lxc image alias create foo "${sum}"
  lxc image alias create bar "${sum}"
  lxc image alias list local: | grep -wF foo
  lxc image alias list local: | grep -wF bar
  !  lxc image alias list local: foo | grep -wF bar || false
  lxc image alias list local: "${sum}" | grep -wF foo
  ! lxc image alias list local: non-existent | grep -wF non-existent || false
  lxc image alias delete foo
  lxc image alias delete bar

  lxc image alias create foo "${sum}"
  lxc image alias rename foo bar
  ! lxc image alias list | grep -wF foo || false  # the old name is gone
  lxc image alias delete bar

  # Test image list output formats (table & json)
  lxc image list --format table | grep -wF testimage
  lxc image list --format json \
    | jq '.[]|select(.alias[0].name="testimage")' \
    | grep -F '"name": "testimage"'

  # Test image delete
  lxc image delete testimage

  # test GET /1.0, since the client always puts to /1.0/
  my_curl -f -X GET "https://${LXD_ADDR}/1.0"
  my_curl -f -X GET "https://${LXD_ADDR}/1.0/containers"

  # Re-import the image
  mv "${LXD_DIR}/${sum}.tar.xz" "${LXD_DIR}/testimage.tar.xz"
  lxc image import "${LXD_DIR}/testimage.tar.xz" --alias testimage user.foo=bar --public
  [ "$(lxc image get-property testimage user.foo)" = "bar" ]
  lxc image show testimage | grep -xF "public: true"
  lxc image delete testimage
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

  sum="$(lxc image info splitimage | awk '/^Fingerprint/ {print $2}')"

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

  # Test --no-profiles flag
  poolName=$(lxc profile device get default root pool)
  ! lxc init testimage foo --no-profiles || false
  lxc init testimage foo --no-profiles -s "${poolName}"
  lxc delete -f foo

  # Test container creation
  lxc init testimage foo
  lxc list | grep foo | grep STOPPED
  lxc list fo | grep foo | grep STOPPED

  echo "Invalid container names"
  ! lxc init --empty ".." || false
  # Escaping `\` multiple times due to `lxc` wrapper script munging the first layer
  ! lxc init --empty "\\\\" || false
  ! lxc init --empty "/" || false
  ! lxc init --empty ";" || false

  echo "Too small containers"
  ! lxc init --empty c1 -c limits.memory=0 || false
  ! lxc init --empty c1 -c limits.memory=0% || false

  echo "Containers with snapshots"
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1
  # Invalid snapshot names
  ! lxc snapshot c1 ".." || false
  # Escaping `\` multiple times due to `lxc` wrapper script munging the first layer
  ! lxc snapshot c1 "\\\\" || false
  ! lxc snapshot c1 "/" || false
  [ "$(lxc list -f csv -c S c1)" = "1" ]
  lxc start c1
  lxc snapshot c1
  [ "$(lxc list -f csv -c S c1)" = "2" ]
  lxc delete --force c1

  # Test list json format
  lxc list --format json | jq '.[]|select(.name="foo")' | grep '"name": "foo"'

  # Test list with --columns and --fast
  ! lxc list --columns=nsp --fast || false

  # Check volatile.apply_template is correct.
  [ "$(lxc config get foo volatile.apply_template)" = "create" ]

  # Start the instance to clear apply_template.
  lxc start foo
  lxc stop foo -f

  # Test container rename
  lxc move foo bar

  # Check volatile.apply_template is altered during rename.
  [ "$(lxc config get bar volatile.apply_template)" = "rename" ]

  [ "$(lxc list -c n | grep -F foo)" = "" ]
  [ "$(lxc list -c n | grep -F bar)" != "" ]

  lxc rename bar foo
  [ "$(lxc list -c n | grep -F bar)" = "" ]
  [ "$(lxc list -c n | grep -F foo)" != "" ]
  lxc rename foo bar

  # Test container copy
  lxc copy bar foo
  lxc delete foo

  # gen untrusted cert
  gen_cert_and_key client3

  # don't allow requests without a cert to get trusted data
  [ "$(curl -k -s -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/containers/foo")" = "403" ]

  # Test unprivileged container publish
  lxc publish bar --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  ! CERTNAME="client3" my_curl -X GET "https://${LXD_ADDR}/1.0/images" | grep -F "/1.0/images/" || false
  lxc image delete foo-image

  # Test container publish with existing alias
  lxc publish bar --alias=foo-image --alias=bar-image2
  lxc launch testimage baz
  # change the container filesystem so the resulting image is different
  lxc exec baz -- touch /somefile
  lxc stop baz --force
  # publishing another image with same alias should fail
  ! lxc publish baz --alias=foo-image || false
  # publishing another image with same alias and '--reuse' flag should success
  lxc publish baz --alias=foo-image --reuse
  fooImage=$(lxc image list -cF -fcsv foo-image)
  barImage2=$(lxc image list -cF -fcsv bar-image2)
  lxc delete baz
  lxc image delete foo-image bar-image2

  # the first image should have bar-image2 alias and the second imgae foo-image alias
  if [ "$fooImage" = "$barImage2" ]; then
    echo "foo-image and bar-image2 aliases should be assigned to two different images"
    false
  fi


  # Test container publish with existing alias
  lxc publish bar --alias=foo-image --alias=bar-image2
  lxc launch testimage baz
  # change the container filesystem so the resulting image is different
  lxc exec baz -- touch /somefile
  lxc stop baz --force
  # publishing another image with same aliases
  lxc publish baz --alias=foo-image --alias=bar-image2 --reuse
  fooImage=$(lxc image list -cF -fcsv foo-image)
  barImage2=$(lxc image list -cF -fcsv bar-image2)
  lxc delete baz
  lxc image delete foo-image

  # the second image should have foo-image and bar-image2 aliases and the first one should be removed
  if [ "$fooImage" != "$barImage2" ]; then
    echo "foo-image and bar-image2 aliases should be assigned to the same image"
    false
  fi


  # Test image compression on publish
  lxc publish bar --alias=foo-image-compressed --compression=bzip2 prop=val1
  lxc image show foo-image-compressed | grep val1
  ! CERTNAME="client3" my_curl -X GET "https://${LXD_ADDR}/1.0/images" | grep -F "/1.0/images/" || false
  lxc image delete foo-image-compressed

  # Test compression options
  lxc publish bar --alias=foo-image-compressed --compression="gzip --rsyncable" prop=val1
  lxc image delete foo-image-compressed

  # Test privileged container publish
  lxc profile create priv
  lxc profile set priv security.privileged true
  lxc init testimage barpriv -p default -p priv
  lxc publish barpriv --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  ! CERTNAME="client3" my_curl -X GET "https://${LXD_ADDR}/1.0/images" | grep -F "/1.0/images/" || false
  lxc image delete foo-image
  lxc delete barpriv
  lxc profile delete priv

  # Test that containers without metadata.yaml are published successfully.
  # Note that this quick hack won't work for LVM, since it doesn't always mount
  # the container's filesystem. That's ok though: the logic we're trying to
  # test here is independent of storage backend, so running it for just one
  # backend (or all non-lvm backends) is enough.
  if [ "$lxd_backend" = "lvm" ]; then
    lxc init testimage nometadata
    rm -f "${LXD_DIR}/containers/nometadata/metadata.yaml"
    lxc publish nometadata --alias=nometadata-image
    lxc image delete nometadata-image
    lxc delete nometadata
  fi

  # Test public images
  lxc publish --public bar --alias=bar-image2
  CERTNAME="client3" my_curl -X GET "https://${LXD_ADDR}/1.0/images" | grep -F "/1.0/images/"
  lxc image delete bar-image2

  # Test invalid instance names
  ! lxc init testimage -abc || false
  ! lxc init testimage abc- || false
  ! lxc init testimage 1234 || false
  ! lxc init testimage foo.bar || false
  ! lxc init testimage a_b_c || false
  ! lxc init testimage aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa || false

  # Test snapshot publish
  lxc snapshot bar
  lxc publish bar/snap0 --alias foo
  lxc init foo bar2
  lxc list -c n | grep bar2
  lxc delete bar2
  lxc image delete foo

  # Test alias support
  cp "${LXD_CONF}/config.yml" "${LXD_CONF}/config.yml.bak"

  #   1. Basic built-in alias functionality
  [ "$(lxc ls)" = "$(lxc list)" ]
  #   2. Basic user-defined alias functionality
  printf "aliases:\\n  l: list\\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc l)" = "$(lxc list)" ]
  #   3. Built-in aliases and user-defined aliases can coexist
  [ "$(lxc ls)" = "$(lxc l)" ]
  #   4. Multi-argument alias keys and values
  echo "  i ls: image list" >> "${LXD_CONF}/config.yml"
  [ "$(lxc i ls)" = "$(lxc image list)" ]
  #   5. Aliases where len(keys) != len(values) (expansion/contraction of number of arguments)
  printf "  ils: image list\\n  container ls: list\\n" >> "${LXD_CONF}/config.yml"
  [ "$(lxc ils)" = "$(lxc image list)" ]
  [ "$(lxc container ls)" = "$(lxc list)" ]
  #   6. User-defined aliases override built-in aliases
  echo "  cp: list" >> "${LXD_CONF}/config.yml"
  [ "$(lxc ls)" = "$(lxc cp)" ]
  #   7. User-defined aliases override commands and don't recurse
  lxc init --empty foo
  LXC_CONFIG_SHOW=$(lxc config show foo --expanded)
  echo "  config show: config show --expanded" >> "${LXD_CONF}/config.yml"
  [ "$(lxc config show foo)" = "$LXC_CONFIG_SHOW" ]
  lxc delete foo

  # Restore the config to remove the aliases
  mv "${LXD_CONF}/config.yml.bak" "${LXD_CONF}/config.yml"

  # Delete the bar container we've used for several tests
  lxc delete bar

  # lxc delete should also delete all snapshots of bar
  [ ! -d "${LXD_DIR}/snapshots/bar" ]

  # Test randomly named container creation
  lxc launch testimage
  RDNAME=$(lxc list --format csv --columns n)
  lxc delete -f "${RDNAME}"

  # Test "nonetype" container creation
  wait_for "${LXD_ADDR}" my_curl -X POST --fail-with-body -H 'Content-Type: application/json' "https://${LXD_ADDR}/1.0/containers" \
        -d "{\"name\":\"nonetype\",\"source\":{\"type\":\"none\"}}"
  lxc delete nonetype

  # Test "nonetype" container creation with an LXC config
  wait_for "${LXD_ADDR}" my_curl -X POST --fail-with-body -H 'Content-Type: application/json' "https://${LXD_ADDR}/1.0/containers" \
        -d "{\"name\":\"configtest\",\"config\":{\"raw.lxc\":\"lxc.hook.clone=/bin/true\"},\"source\":{\"type\":\"none\"}}"
  # shellcheck disable=SC2102
  [ "$(my_curl "https://${LXD_ADDR}/1.0/containers/configtest" | jq -r .metadata.config[\"raw.lxc\"])" = "lxc.hook.clone=/bin/true" ]
  lxc delete configtest

  # Test activateifneeded/shutdown
  LXD_ACTIVATION_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_ACTIVATION_DIR}" true
  (
    set -e
    # shellcheck disable=SC2030
    LXD_DIR=${LXD_ACTIVATION_DIR}
    ensure_import_testimage
    lxd activateifneeded --debug 2>&1 | grep -F "Daemon has core.https_address set, activating..."
    lxc config unset core.https_address --force-local
    ! lxd activateifneeded --debug 2>&1 | grep -F "activating..." || false
    lxc init testimage autostart --force-local
    ! lxd activateifneeded --debug 2>&1 | grep -F "activating..." || false
    lxc config set autostart boot.autostart true --force-local

    # Restart the daemon, this forces the global database to be dumped to disk.
    shutdown_lxd "${LXD_DIR}"
    respawn_lxd "${LXD_DIR}" true
    lxc stop --force autostart --force-local

    lxd activateifneeded --debug 2>&1 | grep -F "Daemon has auto-started instances, activating..."

    lxc config unset autostart boot.autostart --force-local

    # Restart the daemon, this forces the global database to be dumped to disk.
    shutdown_lxd "${LXD_DIR}"
    respawn_lxd "${LXD_DIR}" true

    ! lxd activateifneeded --debug 2>&1 | grep -F "activating..." || false

    lxc start autostart --force-local
    PID="$(lxc list --force-local -f csv -c p autostart)"
    shutdown_lxd "${LXD_DIR}"

    # Stopping LXD should also stop the instances
    ! [ -d "/proc/${PID}" ] || false

    # `lxd activateifneeded` will error out due to LXD being stopped and not having any Unix socket to wake it up
    # but it should also log something about the activation status
    OUTPUT="$(! lxd activateifneeded --debug 2>&1 || false)"
    echo "${OUTPUT}" | grep -F "Daemon has auto-started instances, activating..."

    # shellcheck disable=SC2031
    respawn_lxd "${LXD_DIR}" true

    lxc list --force-local autostart | grep -wF RUNNING

    # Check for scheduled instance snapshots
    lxc stop --force autostart --force-local
    lxc config set autostart snapshots.schedule "* * * * *" --force-local
    shutdown_lxd "${LXD_DIR}"

    # `lxd activateifneeded` will error out due to LXD being stopped and not having any Unix socket to wake it up
    # but it should also log something about the activation status
    OUTPUT="$(! lxd activateifneeded --debug 2>&1 || false)"
    echo "${OUTPUT}" | grep -F "Daemon has scheduled instance snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_lxd "${LXD_DIR}" true

    lxc config unset autostart snapshots.schedule --force-local

    # Check for scheduled volume snapshots
    storage_pool="lxdtest-$(basename "${LXD_DIR}")"

    lxc storage volume create "${storage_pool}" vol --force-local

    shutdown_lxd "${LXD_DIR}"
    ! lxd activateifneeded --debug 2>&1 | grep -F "activating..." || false

    # shellcheck disable=SC2031
    respawn_lxd "${LXD_DIR}" true

    lxc storage volume set "${storage_pool}" vol snapshots.schedule="* * * * *" --force-local

    shutdown_lxd "${LXD_DIR}"

    # `lxd activateifneeded` will error out due to LXD being stopped and not having any Unix socket to wake it up
    # but it should also log something about the activation status
    OUTPUT="$(! lxd activateifneeded --debug 2>&1 || false)"
    echo "${OUTPUT}" | grep -F "Daemon has scheduled volume snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_lxd "${LXD_DIR}" true

    lxc delete autostart --force --force-local
    lxc storage volume delete "${storage_pool}" vol --force-local
  )
  # shellcheck disable=SC2031,2269
  LXD_DIR=${LXD_DIR}
  kill_lxd "${LXD_ACTIVATION_DIR}"

  # Create and start a container
  lxc launch testimage foo
  lxc list | grep foo | grep RUNNING
  lxc stop foo --force

  if lxc info | grep -F 'unpriv_binfmt: "true"'; then
    # Test binfmt_misc support
    lxc start foo
    lxc exec foo -- mount -t binfmt_misc none /proc/sys/fs/binfmt_misc
    [ "$(lxc exec foo -- cat /proc/sys/fs/binfmt_misc/status)" = "enabled" ]
    lxc stop -f foo
  fi

  # cycle it a few times
  lxc start foo
  mac1=$(lxc exec foo -- cat /sys/class/net/eth0/address)
  lxc stop foo --force
  lxc start foo
  mac2=$(lxc exec foo -- cat /sys/class/net/eth0/address)

  if [ -n "${mac1}" ] && [ -n "${mac2}" ] && [ "${mac1}" != "${mac2}" ]; then
    echo "==> MAC addresses didn't match across restarts (${mac1} vs ${mac2})"
    false
  fi

  # Test freeze/pause
  lxc freeze foo
  ! lxc stop foo || false
  lxc stop -f foo
  lxc start foo
  lxc freeze foo
  lxc start foo

  # Test instance types
  lxc init --empty test-limits -t c0.5-m0.2 -d "${SMALL_ROOT_DISK}"
  [ "$(lxc config get test-limits limits.cpu)" = "1" ]
  [ "$(lxc config get test-limits limits.cpu.allowance)" = "50%" ]
  [ "$(lxc config get test-limits limits.memory)" = "204MiB" ]
  lxc delete -f test-limits

  # Test last_used_at field is working properly
  lxc init testimage last-used-at-test
  [ "$(lxc list last-used-at-test --format json | jq -r '.[].last_used_at')" = "1970-01-01T00:00:00Z" ]
  lxc start last-used-at-test
  [ "$(lxc list last-used-at-test --format json | jq -r '.[].last_used_at')" != "1970-01-01T00:00:00Z" ]
  lxc delete last-used-at-test --force

  # Test user, group and cwd
  lxc exec foo -- mkdir /blah
  [ "$(lxc exec foo --user 1000 -- id -u)" = "1000" ]
  [ "$(lxc exec foo --group 1000 -- id -g)" = "1000" ]
  [ "$(lxc exec foo --cwd /blah -- pwd)" = "/blah" ]

  [ "$(lxc exec foo --user 1234 --group 5678 --cwd /blah -- id -u)" = "1234" ]
  [ "$(lxc exec foo --user 1234 --group 5678 --cwd /blah -- id -g)" = "5678" ]
  [ "$(lxc exec foo --user 1234 --group 5678 --cwd /blah -- pwd)" = "/blah" ]

  # check that we can set the environment
  lxc exec foo -- pwd | grep /root
  lxc exec --env BEST_BAND=meshuggah foo -- env | grep -xF BEST_BAND=meshuggah
  lxc exec foo -- ip link show | grep eth0

  # check that environment variables work with profiles
  lxc profile create clash
  lxc profile set clash environment.BEST_BAND=clash
  lxc profile add foo clash
  lxc exec foo -- env | grep -xF BEST_BAND=clash
  lxc exec --env BEST_BAND=meshuggah foo -- env | grep -xF BEST_BAND=meshuggah
  lxc profile remove foo clash
  ! lxc exec foo -- env | grep -F BEST_BAND= || false
  lxc exec --env BEST_BAND=meshuggah foo -- env | grep -xF BEST_BAND=meshuggah
  lxc profile delete clash

  # check that we can get the return code for a non- wait-for-websocket exec
  op=$(my_curl -X POST --fail-with-body -H 'Content-Type: application/json' "https://${LXD_ADDR}/1.0/containers/foo/exec" -d '{"command": ["echo", "test"], "environment": {}, "wait-for-websocket": false, "interactive": false}' | jq -r .operation)
  [ "$(my_curl "https://${LXD_ADDR}${op}/wait" | jq -r .metadata.metadata.return)" != "null" ]

  # test file transfer
  echo abc > "${LXD_DIR}/in"

  lxc file push "${LXD_DIR}/in" foo/root/
  [ "$(lxc exec foo -- /bin/cat /root/in)" = "abc" ]
  lxc exec foo -- /bin/rm -f root/in

  lxc file push "${LXD_DIR}/in" foo/root/in1
  [ "$(lxc exec foo -- /bin/cat /root/in1)" = "abc" ]
  lxc exec foo -- /bin/rm -f root/in1

  # test lxc file edit doesn't change target file's owner and permissions
  echo "content" | lxc file push - foo/tmp/edit_test
  lxc exec foo -- chown 55:55 /tmp/edit_test
  lxc exec foo -- chmod 555 /tmp/edit_test
  echo "new content" | lxc file edit foo/tmp/edit_test
  [ "$(lxc exec foo -- cat /tmp/edit_test)" = "new content" ]
  [ "$(lxc exec foo -- stat -c \"%u %g %a\" /tmp/edit_test)" = "55 55 555" ]

  # make sure stdin is chowned to our container root uid (Issue #590)
  [ -t 0 ] && [ -t 1 ] && lxc exec foo -- chown 1000:1000 /proc/self/fd/0

  echo foo | lxc exec foo -- tee /tmp/foo

  # test exec with/without "--" separator
  lxc exec foo -- true
  lxc exec foo true

  # Detect regressions/hangs in exec
  sum=$(ps aux | tee "${LXD_DIR}/out" | lxc exec foo -- md5sum | cut -d' ' -f1)
  [ "${sum}" = "$(md5sum "${LXD_DIR}/out" | cut -d' ' -f1)" ]
  rm "${LXD_DIR}/out"

  # FIXME: make this backend agnostic
  if [ "$lxd_backend" = "dir" ]; then
    [ "$(< "${LXD_DIR}/containers/foo/rootfs/tmp/foo")" = "foo" ]
  fi

  lxc launch testimage deleterunning
  my_curl -X DELETE "https://${LXD_ADDR}/1.0/containers/deleterunning" | grep "Instance is running"
  lxc delete deleterunning -f

  # cleanup
  lxc delete foo -f

  if [ -e /sys/module/apparmor/ ]; then
    # check that an apparmor profile is created for this container, that it is
    # unloaded on stop, and that it is deleted when the container is deleted
    lxc launch testimage lxd-apparmor-test

    MAJOR=0
    MINOR=0
    if [ -f /sys/kernel/security/apparmor/features/domain/version ]; then
      MAJOR=$(awk -F. '{print $1}' < /sys/kernel/security/apparmor/features/domain/version)
      MINOR=$(awk -F. '{print $2}' < /sys/kernel/security/apparmor/features/domain/version)
    fi

    if [ "${MAJOR}" -gt "1" ] || { [ "${MAJOR}" = "1" ] && [ "${MINOR}" -ge "2" ]; }; then
      aa_namespace="lxd-lxd-apparmor-test_<$(echo "${LXD_DIR}" | sed -e 's/\//-/g' -e 's/^.//')>"
      aa-status | grep -F ":${aa_namespace}:unconfined" || aa-status | grep -F ":${aa_namespace}://unconfined"
      lxc stop lxd-apparmor-test --force
      ! aa-status | grep -F ":${aa_namespace}:" || false
    else
      aa-status | grep "lxd-lxd-apparmor-test_<${LXD_DIR}>"
      lxc stop lxd-apparmor-test --force
      ! aa-status | grep -F "lxd-lxd-apparmor-test_<${LXD_DIR}>" || false
    fi
    lxc delete lxd-apparmor-test
    [ ! -f "${LXD_DIR}/security/apparmor/profiles/lxd-lxd-apparmor-test" ]
  else
    echo "==> SKIP: apparmor tests (missing kernel support)"
  fi

  if [ "$(awk '/^Seccomp:/ {print $2}' "/proc/self/status")" -eq "0" ]; then
    lxc launch testimage lxd-seccomp-test
    init="$(lxc list -f csv -c p lxd-seccomp-test)"
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "2" ]
    lxc stop --force lxd-seccomp-test
    lxc config set lxd-seccomp-test security.syscalls.deny_default false
    lxc start lxd-seccomp-test
    init="$(lxc list -f csv -c p lxd-seccomp-test)"
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "0" ]
    lxc delete --force lxd-seccomp-test
  else
    echo "==> SKIP: seccomp tests (seccomp filtering is externally enabled)"
  fi

  # make sure that privileged containers are not world-readable
  lxc profile create unconfined
  lxc profile set unconfined security.privileged true
  lxc init testimage foo2 -p unconfined -s "lxdtest-$(basename "${LXD_DIR}")"
  [ "$(stat -L -c "%a" "${LXD_DIR}/containers/foo2")" = "100" ]
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
  ! lxc image list | grep -wF image1 || false
  ! lxc image list | grep -wF image2 || false
  ! lxc image list | grep -wF image3 || false
  # Cleanup the containers
  lxc delete --force c1 c2 c3

  # Test --all flag
  lxc init testimage c1
  lxc init testimage c2
  lxc start --all
  lxc list | grep c1 | grep RUNNING
  lxc list | grep c2 | grep RUNNING

  lxc freeze c2
  lxc list | grep c2 | grep FROZEN
  lxc start --all
  lxc list | grep c1 | grep RUNNING
  lxc list | grep c2 | grep RUNNING

  ! lxc stop --all c1 || false
  lxc stop --all -f
  lxc list | grep c1 | grep STOPPED
  lxc list | grep c2 | grep STOPPED
  # Cleanup the containers
  lxc delete --force c1 c2

  # Ephemeral
  lxc launch testimage foo --ephemeral
  OLD_INIT="$(lxc list -f csv -c p foo)"

  REBOOTED="false"

  for _ in $(seq 60); do
    NEW_INIT="$(lxc list -f csv -c p foo)"

    # If init process is running, check if is old or new process.
    if [ -n "${NEW_INIT}" ]; then
      if [ "${OLD_INIT}" != "${NEW_INIT}" ]; then
        REBOOTED="true"
        break
      else
        lxc exec foo -- reboot || true  # Signal to running old init process to reboot if not rebooted yet.
      fi
    fi

    sleep 0.5
  done

  [ "${REBOOTED}" = "true" ]

  lxc publish foo --alias foo --force
  lxc image delete foo

  lxc restart -f foo
  lxc stop foo --force
  ! lxc list | grep -wF foo || false

  # Test renaming/deletion of the default profile
  ! lxc profile rename default foobar || false
  ! lxc profile delete default || false

  lxc init --empty c1
  result="$(! lxc config device override c1 root pool=bla 2>&1)"
  if ! echo "${result}" | grep "Error: Cannot update root disk device pool name"; then
    echo "Should fail device override because root disk device storage pool cannot be changed."
    false
  fi

  lxc delete -f c1

  # Should fail to override root device storage pool when the new pool does not exist.
  ! lxc init testimage c1 -d root,pool=bla || false

  # Should succeed in overriding root device storage pool when the pool does exist and the override occurs at create time.
  lxc storage create bla dir
  lxc init --empty c1 -d root,pool=bla
  lxc config show c1 --expanded | grep -Pz '  root:\n    path: /\n    pool: bla\n    type: disk\n'

  lxc storage volume create bla vol1
  lxc storage volume create bla vol2
  lxc config device add c1 dev disk source=vol1 pool=bla path=/vol

  # Should not be able to override a device that is not part of a profile (i.e. has been specifically added).
  result="$(! lxc config device override c1 dev source=vol2 2>&1)"
  if ! echo "${result}" | grep "Error: The device already exists"; then
    echo "Should fail because device is defined against the instance not the profile."
    false
  fi

  lxc delete -f c1
  lxc storage volume delete bla vol1
  lxc storage volume delete bla vol2
  lxc storage delete bla

  # Test rebuilding an instance with its original image.
  lxc init testimage c1
  lxc start c1
  lxc exec c1 -- touch /data.txt
  lxc stop c1
  lxc rebuild testimage c1
  lxc start c1
  ! lxc exec c1 -- stat /data.txt || false
  lxc delete c1 -f

  # Test a forced rebuild
  lxc launch testimage c1
  ! lxc rebuild testimage c1 || false
  lxc rebuild testimage c1 --force
  lxc delete c1 -f

  # Test rebuilding an instance with a new image.
  lxc init c1 --empty
  lxc rebuild testimage c1
  lxc start c1
  lxc delete c1 -f

  # Test rebuilding an instance with an empty file system.
  lxc init testimage c1
  lxc rebuild c1 --empty
  ! lxc config show c1 | grep -F 'image.' || false
  lxc delete c1 -f

  # Test assigning an empty profile (with no root disk device) to an instance.
  lxc init testimage c1
  lxc profile create foo
  ! lxc profile assign c1 foo || false
  lxc profile delete foo
  lxc delete -f c1

  # Test assigning a profile through a YAML file to an instance.
  poolName=$(lxc profile device get default root pool)
  lxc profile create foo < <(cat <<EOF
config:
  limits.cpu: 2
  limits.memory: 1024MiB
description: Test profile
devices:
  root:
    path: /
    pool: ${poolName}
    type: disk
EOF
)
  lxc init testimage c1 --profile foo
  [ "$(lxc config get c1 limits.cpu --expanded)" = "2" ]
  [ "$(lxc config get c1 limits.memory --expanded)" = "1024MiB" ]
  lxc delete -f c1
  lxc profile delete foo

  # Multiple ephemeral instances delete
  lxc launch testimage c1 --ephemeral
  lxc launch testimage c2 --ephemeral
  lxc launch testimage c3 --ephemeral

  lxc stop -f c1 c2 c3
  [ "$(lxc list -f csv -c n)" = "" ]

  # Cleanup
  fingerprint="$(lxc config trust ls --format csv | cut -d, -f4)"
  lxc config trust remove "${fingerprint}"
}

test_basic_version() {
  for bin in lxc lxd lxd-agent lxd-benchmark lxd-migrate lxd-user fuidshift; do
    "${bin}" --version
    "${bin}" --help
  done

  # lxd subcommands
  for sub in activateifneeded callhook import init manpage migratedump netcat recover shutdown sql version waitready cluster; do
      lxd "${sub}" --help
  done

  # lxd fork subcommands, except for: forkcoresched forkexec forkproxy forksyscall forkuevent
  for sub in forkconsole forkdns forkfile forklimits forkmigrate forksyscallgo forkmount forknet forkstart forkzfs; do
      lxd "${sub}" --help
  done
}

test_server_info() {
  # Ensure server always reports support for containers.
  lxc query /1.0 | jq -e '.environment.instance_types | contains(["container"])'

  # Ensure server reports support for VMs if it should test them.
  if [ "${LXD_VM_TESTS:-0}" = "1" ]; then
    lxc query /1.0 | jq -e '.environment.instance_types | contains(["virtual-machine"])'
  fi

  # Ensure the version number has the format (X.Y.Z for LTSes and X.Y otherwise)
  if lxc query /1.0 | jq -e '.environment.server_lts == true'; then
    lxc query /1.0 | jq -re '.environment.server_version' | grep -E '[0-9]+\.[0-9]+\.[0-9]+'
  else
    lxc query /1.0 | jq -re '.environment.server_version' | grep -xE '[0-9]+\.[0-9]+'
  fi
}

test_duplicate_detection() {
  ensure_import_testimage
  test_image_fingerprint="$(lxc query /1.0/images/aliases/testimage | jq -r '.target')"

  lxc auth group create foo
  [ "$(! "${_LXC}" auth group create foo 2>&1 1>/dev/null)" = 'Error: Authorization group "foo" already exists' ]
  lxc auth group create bar
  [ "$(! "${_LXC}" auth group rename bar foo 2>&1 1>/dev/null)" = 'Error: Authorization group "foo" already exists' ]
  lxc auth group delete foo
  lxc auth group delete bar

  lxc auth identity-provider-group create foo
  [ "$(! "${_LXC}" auth identity-provider-group create foo 2>&1 1>/dev/null)" = 'Error: Identity provider group "foo" already exists' ]
  lxc auth identity-provider-group create bar
  [ "$(! "${_LXC}" auth identity-provider-group rename bar foo 2>&1 1>/dev/null)" = 'Error: Identity provider group "foo" already exists' ]
  lxc auth identity-provider-group delete foo
  lxc auth identity-provider-group delete bar

  lxc auth identity create tls/foo
  [ "$(! "${_LXC}" auth identity create tls/foo 2>&1 1>/dev/null)" = 'Error: An identity with name "foo" already exists' ]
  lxc auth identity delete tls/foo

  lxc project create foo
  [ "$(! "${_LXC}" project create foo 2>&1 1>/dev/null)" = 'Error: Project "foo" already exists' ]
  lxc project create bar
  [ "$(! "${_LXC}" project rename bar foo 2>&1 1>/dev/null)" = 'Error: A project named "foo" already exists' ]
  lxc project delete foo
  lxc project delete bar

  [ "$(! "${_LXC}" image alias create testimage "${test_image_fingerprint}" 2>&1 1>/dev/null)" = 'Error: Alias "testimage" already exists' ]

  lxc init foo --empty
  [ "$(! "${_LXC}" init foo --empty 2>&1 1>/dev/null)" = 'Error: Failed creating instance record: Instance "foo" already exists' ]
  lxc init bar --empty
  [ "$(! "${_LXC}" rename bar foo 2>&1 1>/dev/null)" = 'Error: Name "foo" already in use' ]
  lxc delete bar

  lxc snapshot foo snap0
  [ "$(! "${_LXC}" snapshot foo snap0 2>&1 1>/dev/null)" = 'Error: Failed creating instance snapshot record "snap0": Snapshot "foo/snap0" already exists' ]
  lxc snapshot foo snap1
  [ "$(! "${_LXC}" rename foo/snap1 foo/snap0 2>&1 1>/dev/null)" = 'Error: Name "foo/snap0" already in use' ]
  lxc delete foo/snap0
  lxc delete foo/snap1
  lxc delete foo

  lxc network create foo
  [ "$(! "${_LXC}" network create foo 2>&1 1>/dev/null)" = 'Error: The network already exists' ]
  lxc network create bar ipv4.address=none ipv6.address=none
  [ "$(! "${_LXC}" network rename bar foo 2>&1 1>/dev/null)" = 'Error: Network "foo" already exists' ]
  lxc network delete bar

  lxc network acl create foo
  [ "$(! "${_LXC}" network acl create foo 2>&1 1>/dev/null)" = 'Error: The network ACL already exists' ]
  lxc network acl create bar
  [ "$(! "${_LXC}" network acl rename bar foo 2>&1 1>/dev/null)" = 'Error: An ACL by that name exists already' ]
  lxc network acl delete foo
  lxc network acl delete bar

  lxc network zone create foo
  [ "$(! "${_LXC}" network zone create foo 2>&1 1>/dev/null)" = 'Error: The network zone already exists' ]
  lxc network zone delete foo

  lxc network forward create foo 10.1.1.1
  [ "$(! "${_LXC}" network forward create foo 10.1.1.1 2>&1 1>/dev/null)" = 'Error: Failed creating forward: A forward for that listen address already exists' ]
  lxc network forward delete foo 10.1.1.1

  lxc network forward create foo 2001:db8::1
  [ "$(! "${_LXC}" network forward create foo 2001:db8::1 2>&1 1>/dev/null)" = 'Error: Failed creating forward: A forward for that listen address already exists' ]
  lxc network forward delete foo 2001:db8::1

  lxc network delete foo

  if ovn_enabled; then
    setup_ovn
    uplink_network="uplink$$"
    ip link add dummy0 type dummy
    lxc network create "${uplink_network}" --type=physical parent=dummy0
    lxc network set "${uplink_network}" ipv4.ovn.ranges=192.0.2.100-192.0.2.254
    lxc network set "${uplink_network}" ipv6.ovn.ranges=2001:db8:1:2::100-2001:db8:1:2::254
    lxc network set "${uplink_network}" ipv4.routes=192.0.2.0/24
    lxc network set "${uplink_network}" ipv6.routes=2001:db8:1:2::/64
    lxc network create foo-ovn --type ovn network="${uplink_network}"

    lxc network load-balancer create foo-ovn 192.0.2.10
    [ "$(! "${_LXC}" network load-balancer create foo-ovn 192.0.2.10 2>&1 1>/dev/null)" = 'Error: Failed creating load balancer: Listen address "192.0.2.10" overlaps with another network or NIC' ]
    lxc network load-balancer delete foo-ovn 192.0.2.10

    lxc network create foo-ovn2 --type ovn network="${uplink_network}"
    lxc network peer create foo-ovn foo foo-ovn2
    [ "$(! "${_LXC}" network peer create foo-ovn foo foo-ovn2 2>&1 1>/dev/null)" = 'Error: Failed creating peer: A peer for that name already exists' ]
    lxc network peer delete foo-ovn foo

    lxc network delete foo-ovn
    lxc network delete foo-ovn2
    lxc network delete "${uplink_network}"
    ip link delete dummy0
    unset_ovn_configuration
  fi

  lxc profile create foo
  [ "$(! "${_LXC}" profile create foo 2>&1 1>/dev/null)" = 'Error: Error inserting "foo" into database: The profile already exists' ]
  lxc profile create bar
  [ "$(! "${_LXC}" profile rename bar foo 2>&1 1>/dev/null)" = 'Error: Name "foo" already in use' ]
  lxc profile delete foo
  lxc profile delete bar

  lxc storage create foo dir
  [ "$(! "${_LXC}" storage create foo dir 2>&1 1>/dev/null)" = 'Error: Storage pool "foo" already exists' ]

  lxc storage bucket create foo foo
  [ "$(! "${_LXC}" storage bucket create foo foo 2>&1 1>/dev/null)" = 'Error: Failed creating storage bucket: Failed inserting storage bucket "foo" for project "default" in pool "foo" into database: A bucket for that name already exists' ]
  lxc storage bucket delete foo foo

  lxc storage volume create foo foo
  [ "$(! "${_LXC}" storage volume create foo foo 2>&1 1>/dev/null)" = 'Error: Volume by that name already exists' ]
  lxc storage volume create foo bar
  [ "$(! "${_LXC}" storage volume rename foo bar foo 2>&1 1>/dev/null)" = 'Error: Volume by that name already exists' ]
  lxc storage volume delete foo bar

  lxc storage volume snapshot foo foo snap0
  [ "$(! "${_LXC}" storage volume snapshot foo foo snap0 2>&1 1>/dev/null)" = 'Error: Snapshot "snap0" already in use' ]
  lxc storage volume snapshot foo foo snap1
  [ "$(! "${_LXC}" storage volume rename foo foo/snap1 foo/snap0 2>&1 1>/dev/null)" = 'Error: Storage volume snapshot "snap0" already exists for volume "foo"' ]
  lxc storage volume delete foo foo/snap0
  lxc storage volume delete foo foo/snap1
  lxc storage volume delete foo foo
  lxc storage delete foo
}
