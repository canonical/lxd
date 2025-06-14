# Test setup helper functions.

ensure_has_localhost_remote() {
    local addr="${1}"
    if ! lxc remote list | grep -wF "localhost" >/dev/null; then
        lxc remote add localhost "https://${addr}" --accept-certificate --password foo
    fi
}

ensure_import_testimage() {
    if lxc image alias list testimage | grep -wF "testimage" >/dev/null; then
        return
    fi

    if [ -e "${LXD_TEST_IMAGE:-}" ]; then
        echo "Importing ${LXD_TEST_IMAGE} test image from disk"
        lxc image import "${LXD_TEST_IMAGE}" --alias testimage
    else
        BUSYBOX="$(command -v busybox)"
        if [ ! -e "${BUSYBOX}" ]; then
            echo "Please install busybox (busybox-static) or set LXD_TEST_IMAGE"
            exit 1
        fi

        if ldd "${BUSYBOX}" >/dev/null 2>&1; then
            echo "The testsuite requires ${BUSYBOX} to be a static binary"
            exit 1
        fi

        project="$(lxc project list | awk '/(current)/ {print $2}')"
        deps/import-busybox --alias testimage --project "$project"
    fi
}
