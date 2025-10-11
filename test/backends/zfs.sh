zfs_setup() {
  local LXD_DIR="${1}"

  echo "==> Setting up ZFS backend in ${LXD_DIR}"
}

zfs_configure() {
  local LXD_DIR="${1}"
  local POOL_NAME="${2:-"lxdtest-${LXD_DIR##*/}"}" # Use the last part of the LXD_DIR as pool name

  echo "==> Configuring ZFS backend in ${LXD_DIR}"

  lxc storage create "${POOL_NAME}" zfs size="${DEFAULT_POOL_SIZE}"
  lxc profile device add default root disk path="/" pool="${POOL_NAME}"
}

zfs_teardown() {
  local LXD_DIR="${1}"

  echo "==> Tearing down ZFS backend in ${LXD_DIR}"
}
