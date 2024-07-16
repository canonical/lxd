# Test setup helper functions.

ensure_has_localhost_remote() {
    local addr="${1}"
    if ! lxc remote list | grep -q "localhost"; then
        token="$(lxc config trust add --name foo -q)"
        lxc remote add localhost "https://${addr}" --accept-certificate --token "${token}"
    fi
}

ensure_import_testimage() {
    if ! lxc image alias list | grep -q "^| testimage\\s*|.*$"; then
        if [ -e "${LXD_TEST_IMAGE:-}" ]; then
            lxc image import "${LXD_TEST_IMAGE}" --alias testimage
        else
            if [ ! -e "/bin/busybox" ]; then
                echo "Please install busybox (busybox-static) or set LXD_TEST_IMAGE"
                exit 1
            fi

            if ldd /bin/busybox >/dev/null 2>&1; then
                echo "The testsuite requires /bin/busybox to be a static binary"
                exit 1
            fi

            project="$(lxc project list | awk '/(current)/ {print $2}')"
            deps/import-busybox --alias testimage --project "$project"
        fi
    fi
}
