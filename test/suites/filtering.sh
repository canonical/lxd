# Test API filtering.
test_filtering() {
    ensure_import_testimage

    lxc init testimage c1
    lxc init testimage c2

    [ "$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=0" --data-urlencode "filter=name eq c1" | jq ".metadata | length")" = "1" ]

    [ "$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=1" --data-urlencode "filter=name eq c1" | jq ".metadata | length")" = "1" ]

    [ "$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=2" --data-urlencode "filter=name eq c1" | jq ".metadata | length")" = "1" ]

    [ "$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/images" --data-urlencode "recursion=0" --data-urlencode "filter=properties.os eq BusyBox" | jq ".metadata | length")" = "1" ]

    [ "$(curl -G --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/images" --data-urlencode "recursion=1" --data-urlencode "filter=properties.os eq Ubuntu" | jq ".metadata | length")" = "0" ]

    lxc delete c1
    lxc delete c2
}
