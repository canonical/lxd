# Test setup helper functions.

ensure_has_localhost_remote() {
    local addr="${1}"
    local token=""
    if ! lxc remote list -f csv | grep -wF "localhost" >/dev/null; then
        token="$(lxc config trust add --name foo -q)"
        lxc remote add localhost "https://${addr}" --token "${token}"
    fi
}

ensure_import_testimage() {
    local project=""
    if lxc image alias list -f csv testimage | grep -wF "testimage" >/dev/null; then
        return
    fi

    if [ -e "${LXD_TEST_IMAGE:-}" ]; then
        echo "Importing ${LXD_TEST_IMAGE} test image from disk"
        lxc image import "${LXD_TEST_IMAGE}" --alias testimage
    else
        project="$(lxc project list -f csv | awk '/(current)/ {print $1}')"
        deps/import-busybox --alias testimage --project "$project"
    fi
}

install_tools() {
    local pkg="${1}"

    if ! check_dependencies "${pkg}" && command -v apt-get >/dev/null; then
        apt-get install --no-install-recommends -y "${pkg}"
    fi

    check_dependencies "${pkg}"
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

install_instance_drivers() {
    # ATM, only VMs require some extra tooling
    if [ "${LXD_VM_TESTS:-0}" = "0" ]; then
        return
    fi

    local UNAME
    local QEMU_SYSTEM

    UNAME="$(uname -m)"
    if [ "${UNAME}" = "x86_64" ]; then
        QEMU_SYSTEM="qemu-system-x86"
    elif [ "${UNAME}" = "aarch64" ]; then
        QEMU_SYSTEM="qemu-system-arm"
    else
        echo "Unable to find the right QEMU system package for: ${UNAME}"
        exit 1
    fi

    if ! check_dependencies qemu-img "qemu-system-${UNAME}" sgdisk make-bcache /usr/lib/qemu/virtiofsd && command -v apt-get >/dev/null; then
        # On 22.04, QEMU comes with spice modules and virtiofsd
        if grep -qxF 'VERSION_ID="22.04"' /etc/os-release; then
            apt-get install --no-install-recommends -y gdisk ovmf qemu-block-extra "${QEMU_SYSTEM}" qemu-utils bcache-tools
        else
            apt-get install --no-install-recommends -y gdisk ovmf qemu-block-extra "${QEMU_SYSTEM}" qemu-utils qemu-system-modules-spice virtiofsd bcache-tools
        fi

        # Verify that the newly installed tools provided the needed binaries
        check_dependencies qemu-img "qemu-system-${UNAME}" sgdisk /usr/lib/qemu/virtiofsd
    fi
}
