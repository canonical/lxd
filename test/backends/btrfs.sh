btrfs_setup() {
  local LXD_DIR="${1}"

  echo "==> Setting up btrfs backend in ${LXD_DIR}"
}

btrfs_configure() {
  local LXD_DIR="${1}"
  local POOL_NAME="${2:-"lxdtest-${LXD_DIR##*/}"}" # Use the last part of the LXD_DIR as pool name

  lxc storage create "${POOL_NAME}" btrfs size="${DEFAULT_POOL_SIZE}"
  lxc profile device add default root disk path="/" pool="${POOL_NAME}"

  echo "==> Configuring btrfs backend in ${LXD_DIR}"
}

btrfs_teardown() {
  local LXD_DIR="${1}"

  echo "==> Tearing down btrfs backend in ${LXD_DIR}"
}
