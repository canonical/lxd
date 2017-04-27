btrfs_setup() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Setting up btrfs backend in ${LXD_DIR}"
}

btrfs_configure() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  lxc storage create "lxdtest-$(basename "${LXD_DIR}")" btrfs size=100GB
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"

  echo "==> Configuring btrfs backend in ${LXD_DIR}"
}

btrfs_teardown() {
  # shellcheck disable=2039
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Tearing down btrfs backend in ${LXD_DIR}"
}
