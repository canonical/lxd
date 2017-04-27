btrfs_setup() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Setting up btrfs backend in ${LXD_DIR}"

  truncate -s 100G "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs"
  mkfs.btrfs "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs"

  mount -o loop "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs" "${LXD_DIR}"
}

btrfs_configure() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Configuring btrfs backend in ${LXD_DIR}"
}

btrfs_teardown() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Tearing down btrfs backend in ${LXD_DIR}"

  umount -l "${LXD_DIR}"
  rm -f "${TEST_DIR}/$(basename "${LXD_DIR}").btrfs"
}
