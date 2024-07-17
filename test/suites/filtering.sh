# Test API filtering.
test_filtering() {
  local LXD_DIR

  LXD_FILTERING_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  spawn_lxd "${LXD_FILTERING_DIR}" true

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    LXD_DIR="${LXD_FILTERING_DIR}"

    ensure_import_testimage

    lxc init testimage c1
    lxc init testimage c2

    count=$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=0" --data-urlencode "filter=name eq c1" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=1" --data-urlencode "filter=name eq c1" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=2" --data-urlencode "filter=name eq c1" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/images" --data-urlencode "recursion=0" --data-urlencode "filter=properties.os eq BusyBox" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/images" --data-urlencode "recursion=1" --data-urlencode "filter=properties.os eq Ubuntu" | jq ".metadata | length")
    [ "${count}" = "0" ] || false

    lxc delete c1
    lxc delete c2
  )

  kill_lxd "${LXD_FILTERING_DIR}"
}
