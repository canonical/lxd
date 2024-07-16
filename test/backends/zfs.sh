zfs_setup() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Setting up ZFS backend in ${LXD_DIR}"
}

zfs_configure() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Configuring ZFS backend in ${LXD_DIR}"

  lxc storage create "lxdtest-$(basename "${LXD_DIR}")" zfs size=1GiB
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"
}

zfs_teardown() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Tearing down ZFS backend in ${LXD_DIR}"
}
