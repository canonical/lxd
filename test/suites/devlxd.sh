test_devlxd() {
  ensure_import_testimage
  fingerprint="$(lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  # Ensure testimage is not set as cached.
  lxd sql global "UPDATE images SET cached=0 WHERE fingerprint=\"${fingerprint}\""

  lxc launch testimage devlxd -c security.devlxd=false -c boot.autostart=true

  ! lxc exec devlxd -- test -S /dev/lxd/sock || false
  lxc config unset devlxd security.devlxd
  lxc exec devlxd -- test -S /dev/lxd/sock
  lxc file push --quiet "$(command -v devlxd-client)" devlxd/bin/

  # Try to get a host's private image from devlxd.
  [ "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" = "Forbidden" ]
  lxc config set devlxd security.devlxd.images true
  # Trying to get a private image should return a not found error so that the client can't infer the existence
  # of an image with the provided fingerprint.
  [ "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}")" = "Not Found" ]
  lxd sql global "UPDATE images SET cached=1 WHERE fingerprint=\"${fingerprint}\""
  # No output means the export succeeded.
  [ -z "$(lxc exec devlxd -- devlxd-client image-export "${fingerprint}" || echo fail)" ]

  lxc config set devlxd user.foo=bar user.xyz="bar %s bar"
  [ "$(lxc exec devlxd -- devlxd-client user.foo)" = "bar" ]
  [ "$(lxc exec devlxd -- devlxd-client user.xyz)" = "bar %s bar" ]

  # Make sure instance configuration keys are not accessible
  [ "$(lxc exec devlxd -- devlxd-client security.nesting)" = "Forbidden" ]
  lxc config set devlxd security.nesting true
  [ "$(lxc exec devlxd -- devlxd-client security.nesting)" = "Forbidden" ]

  "${_LXC}" exec devlxd -- devlxd-client monitor-websocket > "${TEST_DIR}/devlxd-websocket.log" &
  client_websocket=$!

  "${_LXC}" exec devlxd -- devlxd-client monitor-stream > "${TEST_DIR}/devlxd-stream.log" &
  client_stream=$!

  EXPECTED_MD5="$(md5sum - << EOF
{
  "type": "config",
  "timestamp": "0001-01-01T00:00:00Z",
  "metadata": {
    "key": "user.foo",
    "old_value": "bar",
    "value": "baz"
  }
}
{
  "type": "device",
  "timestamp": "0001-01-01T00:00:00Z",
  "metadata": {
    "action": "added",
    "config": {
      "path": "/mnt",
      "source": "${TEST_DIR}",
      "type": "disk"
    },
    "name": "mnt"
  }
}
{
  "type": "device",
  "timestamp": "0001-01-01T00:00:00Z",
  "metadata": {
    "action": "removed",
    "config": {
      "path": "/mnt",
      "source": "${TEST_DIR}",
      "type": "disk"
    },
    "name": "mnt"
  }
}
EOF
)"

  MATCH=0

  for _ in $(seq 10); do
    lxc config set devlxd user.foo=bar security.nesting=true

    true > "${TEST_DIR}/devlxd-websocket.log"
    true > "${TEST_DIR}/devlxd-stream.log"

    lxc config set devlxd user.foo=baz security.nesting=false
    lxc config device add devlxd mnt disk source="${TEST_DIR}" path=/mnt
    lxc config device remove devlxd mnt

    if [ "$(tr -d '\0' < "${TEST_DIR}/devlxd-websocket.log" | md5sum)" != "${EXPECTED_MD5}" ] || [ "$(tr -d '\0' < "${TEST_DIR}/devlxd-stream.log" | md5sum)" != "${EXPECTED_MD5}" ]; then
      sleep 0.5
      continue
    fi

    MATCH=1
    break
  done

  kill -9 "${client_websocket}"
  kill -9 "${client_stream}"

  lxc monitor --type=lifecycle > "${TEST_DIR}/devlxd.log" &
  monitorDevlxdPID=$!

  # Test instance Ready state
  [ "$(lxc list -f csv -c s devlxd)" = "RUNNING" ]
  lxc exec devlxd -- devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "1" ]

  [ "$(lxc list -f csv -c s devlxd)" = "READY" ]
  lxc exec devlxd -- devlxd-client ready-state false
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "false" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "1" ]

  [ "$(lxc list -f csv -c s devlxd)" = "RUNNING" ]

  kill -9 "${monitorDevlxdPID}"
  rm "${TEST_DIR}/devlxd.log"

  # Expedite LXD shutdown by forcibly killing the running instance
  lxc stop -f devlxd

  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # volatile.last_state.ready should be unset during daemon init
  [ -z "$(lxc config get devlxd volatile.last_state.ready || echo fail)" ]

  lxc monitor --type=lifecycle > "${TEST_DIR}/devlxd.log" &
  monitorDevlxdPID=$!

  lxc exec devlxd -- devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "1" ]

  lxc stop -f devlxd
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "false" ]

  lxc start devlxd
  lxc exec devlxd -- devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  [ "$(grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log")" = "2" ]

  # Check device configs are available and that NIC hwaddr is available even if volatile.
  hwaddr=$(lxc config get devlxd volatile.eth0.hwaddr)
  [ "$(lxc exec devlxd -- devlxd-client devices | jq --exit-status --raw-output .eth0.hwaddr)" = "${hwaddr}" ]

  lxc delete devlxd --force
  kill -9 "${monitorDevlxdPID}"
  rm "${TEST_DIR}/devlxd.log"

  [ "${MATCH}" = "1" ]
}

test_devlxd_vm() {
  lxc launch ubuntu-minimal-daily:24.04 v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  waitInstanceReady v1

  echo "==> Check that devlxd is enabled by default and works"
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/devices | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/config | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'instance-id:'
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'local-hostname:'

  # Run sync before forcefully restarting the VM otherwise the filesystem will be corrupted.
  lxc exec v1 -- "sync"
  lxc restart -f v1
  waitInstanceReady v1

  echo "==> Check that devlxd is working after a restart"
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/devices | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/config | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'instance-id:'
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data | grep -F 'local-hostname:'

  echo "==> Check that devlxd is not working once disabled"
  lxc config set v1 security.devlxd false
  ! lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 || false

  echo "==> Check that devlxd can be enabled live"
  lxc config set v1 security.devlxd true
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0 | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/devices | jq
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/config | jq

  echo "==> Ensure that the output metadata is in correct format"
  META_DATA="$(lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock http://custom.socket/1.0/meta-data)"
  [ "$(grep -cxE 'instance-id: [^ ]{36}|local-hostname: v1' <<< "${META_DATA}" || echo fail)" = "2" ]
  [ "$(wc -l <<< "${META_DATA}" || echo fail)" = "2" ]

  echo "==> Test cloud-init user-data"
  # Ensure the header is preserved and the output value is not escaped.
  cloudInitUserData='#cloud-config
package_update: false
package_upgrade: false
runcmd:
- echo test'

  lxc config set v1 cloud-init.user-data "${cloudInitUserData}"
  out="$(lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock lxd/1.0/config/cloud-init.user-data)"
  [ "${out}" = "${cloudInitUserData}" ]
  lxc config unset v1 cloud-init.user-data

  echo "===> Test instance Ready state"
  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock -X PATCH -d '{"state":"Ready"}' http://custom.socket/1.0
  [ "$(lxc config get v1 volatile.last_state.ready)" = "true" ]

  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock -X PATCH -d '{"state":"Started"}' http://custom.socket/1.0
  [ "$(lxc config get v1 volatile.last_state.ready)" = "false" ]

  lxc exec v1 -- curl -s --unix-socket /dev/lxd/sock -X PATCH -d '{"state":"Ready"}' http://custom.socket/1.0
  [ "$(lxc config get v1 volatile.last_state.ready)" = "true" ]
  lxc stop -f v1
  [ "$(lxc config get v1 volatile.last_state.ready)" = "false" ]

  # TODO: add nested virt part from lxd-ci test

  # Cleanup
  lxc image delete "$(lxc config get v1 volatile.base_image)"
  lxc delete v1
}
