# Test setup helper functions.

ensure_has_localhost_remote() {
    local addr="${1}"
    if ! lxc remote list | grep -wF "localhost" >/dev/null; then
        token="$(lxc config trust add --name foo -q)"
        lxc remote add localhost "https://${addr}" --token "${token}"
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

install_storage_driver_tools() {
    # Default to dir backend if none is specified
    # If the requested backend is specified but the needed tooling is missing, try to install it.
    if [ -z "${LXD_BACKEND:-}" ]; then
        LXD_BACKEND="dir"
    elif ! is_backend_available "${LXD_BACKEND}"; then
        pkg=""
        case "${LXD_BACKEND}" in
          ceph)
            pkg="ceph-common";;
          lvm)
            pkg="lvm2";;
          zfs)
            pkg="zfsutils-linux";;
          *)
            ;;
        esac

        if [ -n "${pkg}" ] && command -v apt-get >/dev/null; then
            apt-get install --no-install-recommends -y "${pkg}"

            # Verify that the newly installed tools made the storage backend available
            is_backend_available "${LXD_BACKEND}"
        fi
    fi
}
