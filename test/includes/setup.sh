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
    local project="${1:-}"
    local alias="testimage"

    # Using `--project ""` causes `lxc` to interact with the current project.
    if lxc image alias list -f csv --project "${project}" "${alias}" | grep "^${alias}," >/dev/null; then
        return
    fi

    if [ -e "${LXD_TEST_IMAGE:-}" ]; then
        lxc image import --quiet "${LXD_TEST_IMAGE}" --alias "${alias}" --project "${project}"
    else
        if [ "${project:-}" = "" ]; then
          project="$(lxc project list -f csv | awk '/(current)/ {print $1}')"
        fi
        deps/import-busybox --alias "${alias}" --project "${project}"
    fi
}

# XXX: do not use directly, use ensure_import_ubuntu_image or ensure_import_ubuntu_vm_image instead.
_import_ubuntu_image() {
    local alias="ubuntu"
    local data_file="ubuntu.squashfs"
    if [ "${1:-}" = "--vm" ]; then
        shift
        alias="ubuntu-vm"
        data_file="ubuntu.img"
    else
        echo "Please re-enable the download of the official Ubuntu container image if you see this message"
        echo "To do so, revert the commit introducing this very message"
        false
    fi
    local project="${1:-}"

    # Using `--project ""` causes `lxc` to interact with the current project.
    if lxc image alias list -f csv --project "${project}" "${alias}" | grep "^${alias}," >/dev/null; then
        return
    fi

    local dir="${IMAGE_CACHE_DIR:-${HOME}/image-cache}"
    if [ ! -d "${dir}" ] || [ -z "$(ls -A "${dir}" || echo fail)" ]; then
        echo "Downloading ubuntu test images to cache"
        download_test_images
    fi

    echo "Importing ${alias} test image from cache"
    lxc image import --quiet "${dir}/ubuntu.metadata" "${dir}/${data_file}" --alias "${alias}" --project "${project}"
}

# ensure_import_ubuntu_image: imports the ubuntu (container) test image if not already present.
ensure_import_ubuntu_image() {
    _import_ubuntu_image "$@"
}

# ensure_import_ubuntu_vm_image: imports the ubuntu-vm test image if not already present.
ensure_import_ubuntu_vm_image() {
    _import_ubuntu_image --vm "$@"
}

# download_test_images: downloads external test images and stores them in the cache directory.
download_test_images() {
    local distro="noble"
    local dir="${IMAGE_CACHE_DIR:-${HOME}/image-cache}"
    local base_url="https://cloud-images.ubuntu.com/daily/server/minimal/daily/${distro}/current"

    [ -d "${dir}" ] || mkdir -p "${dir}"
    (
        set -eux
        cd "${dir}"
        # Delete any image older than 1 day
        find . -type f -mtime +1 -delete

        local arch
        arch="${ARCH:-$(dpkg --print-architecture || echo "amd64")}"

        # For containers: .squashfs (rootfs) and the -lxd.tar.xz (metadata) files are needed.
        # For VMs: .img (primary disk) and the -lxd.tar.xz (metadata) files are needed.
        exec curl --show-error --silent --retry 3 --retry-delay 5 \
          --continue-at - "${base_url}/${distro}-minimal-cloudimg-${arch}-lxd.tar.xz" --output "ubuntu.metadata" \
          --continue-at - "${base_url}/${distro}-minimal-cloudimg-${arch}.img"        --output "ubuntu.img"
    )
}

# download_virtiofsd: copies or downloads the virtiofsd binary to the expected location.
download_virtiofsd() {
    local dir="${1:-/usr/lib/qemu}"
    [ -d "${dir}" ] || mkdir -p "${dir}"

    # If virtiofsd is already present in PATH move it to the expected location. Otherwise,
    # download the virtiofsd binary from the latest GitLab CI build artifacts
    if command -v virtiofsd >/dev/null && [ "$(command -v virtiofsd)" != "${dir}/virtiofsd" ]; then
        mv "$(command -v virtiofsd)" "${dir}/virtiofsd"
    else
        curl --show-error --silent --retry 3 --retry-delay 5 --location \
             --continue-at - "https://gitlab.com/virtio-fs/virtiofsd/-/jobs/artifacts/main/raw/target/$(uname -m)-unknown-linux-musl/release/virtiofsd?job=publish" --output "${dir}/virtiofsd"
    fi

    chmod +x "${dir}/virtiofsd"
}

install_packages() {
    if ! command -v apt-get >/dev/null; then
        echo "apt-get not found, cannot install packages"
        exit 1
    fi

    sudo apt-get install --no-install-recommends -y "$@"
}

install_tools() {
    local pkg="${1}"

    if ! check_dependencies "${pkg}"; then
        install_packages "${pkg}"
    fi

    check_dependencies "${pkg}"
}


install_storage_driver_tools() {
    is_backend_available "${LXD_BACKEND}" && return 0

    # Install the needed tooling is missing
    pkg=""
    case "${LXD_BACKEND}" in
      btrfs)
        pkg="btrfs-progs";;
      ceph)
        pkg="ceph-common";;
      lvm)
        pkg="lvm2";;
      zfs)
        pkg="zfsutils-linux";;
      *)
        ;;
    esac

    if [ -n "${pkg}" ]; then
        install_packages "${pkg}"

        # Verify that the newly installed tools made the storage backend available
        is_backend_available "${LXD_BACKEND}"

        # Import storage backends now that new tools are available
        import_storage_backends
    fi
}

install_instance_drivers() {
    # ATM, only VMs require some extra tooling
    if [ "${LXD_VM_TESTS:-1}" = "0" ]; then
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

    if ! check_dependencies qemu-img "qemu-system-${UNAME}" sgdisk make-bcache /usr/lib/qemu/virtiofsd; then
        # On 22.04, QEMU comes with spice modules and virtiofsd
        if grep -qxF 'VERSION_ID="22.04"' /etc/os-release; then
            install_packages gdisk ovmf qemu-block-extra "${QEMU_SYSTEM}" qemu-utils bcache-tools
        else
            install_packages gdisk ovmf qemu-block-extra "${QEMU_SYSTEM}" qemu-utils qemu-system-modules-spice virtiofsd bcache-tools
        fi

        # Verify that the newly installed tools provided the needed binaries
        check_dependencies qemu-img "qemu-system-${UNAME}" sgdisk /usr/lib/qemu/virtiofsd make-bcache
    fi

    # While virtiofsd is present in 22.04's QEMU, it is too old to work properly with LXD so
    # download a more recent version.
    if grep -qxF 'VERSION_ID="22.04"' /etc/os-release; then
        download_virtiofsd "/usr/lib/qemu"

        check_dependencies /usr/lib/qemu/virtiofsd
    fi
}
