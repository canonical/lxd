# Test API filtering.
test_filtering() {
    echo "===> Instance filtering"
    lxc init --empty c1
    lxc init --empty c2
    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=0" --data-urlencode "filter=name eq c1" | jq --exit-status ".metadata | length == 1"
    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=1" --data-urlencode "filter=name eq c1" | jq --exit-status ".metadata | length == 1"
    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/instances" --data-urlencode "recursion=2" --data-urlencode "filter=name eq c1" | jq --exit-status ".metadata | length == 1"

    echo "===> Image filtering"
    ensure_import_testimage
    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/images" --data-urlencode "recursion=0" --data-urlencode "filter=properties.os eq BusyBox" | jq --exit-status ".metadata | length == 1"
    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/images" --data-urlencode "recursion=1" --data-urlencode "filter=properties.os eq Ubuntu" | jq --exit-status ".metadata == []"

    # Cleanup
    lxc delete c1 c2
}
