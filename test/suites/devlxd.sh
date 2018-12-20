test_devlxd() {
  ensure_import_testimage

  # shellcheck disable=SC2164
  cd "${TEST_DIR}"
  go build -tags netgo -a -installsuffix devlxd ../deps/devlxd-client.go
  # shellcheck disable=SC2164
  cd -

  lxc launch testimage devlxd -c security.devlxd=false

  ! lxc exec devlxd -- test -S /dev/lxd/sock
  lxc config unset devlxd security.devlxd
  lxc exec devlxd -- test -S /dev/lxd/sock
  lxc file push "${TEST_DIR}/devlxd-client" devlxd/bin/

  lxc exec devlxd chmod +x /bin/devlxd-client

  lxc config set devlxd user.foo bar
  lxc exec devlxd devlxd-client user.foo | grep bar

  lxc config set devlxd security.nesting true
  ! lxc exec devlxd devlxd-client security.nesting | grep true

  lxc exec devlxd devlxd-client monitor > "${TEST_DIR}/devlxd.log" &
  client=$!

  (
    cat << EOF
metadata:
  key: user.foo
  old_value: bar
  value: baz
timestamp: null
type: config

metadata:
  action: added
  config:
    path: /mnt
    source: ${TEST_DIR}
    type: disk
  name: mnt
timestamp: null
type: device

metadata:
  action: removed
  config:
    path: /mnt
    source: ${TEST_DIR}
    type: disk
  name: mnt
timestamp: null
type: device

EOF
  ) > "${TEST_DIR}/devlxd.expected"

  MATCH=0

  # shellcheck disable=SC2034
  for i in $(seq 5); do
    lxc config set devlxd user.foo bar
    lxc config set devlxd security.nesting true

    true > "${TEST_DIR}/devlxd.log"

    lxc config set devlxd user.foo baz
    lxc config set devlxd security.nesting false
    lxc config device add devlxd mnt disk source="${TEST_DIR}" path=/mnt
    lxc config device remove devlxd mnt

    if [ "$(tr -d '\0' < "${TEST_DIR}/devlxd.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/devlxd.expected" | cut -d' ' -f1)" ]; then
      sleep 1
      continue
    fi

    MATCH=1
    break
  done

  kill -9 "${client}"
  lxc delete devlxd --force

  [ "${MATCH}" = "1" ] || false
}
