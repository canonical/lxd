lvm_setup() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Setting up lvm backend in ${LXD_DIR}"
}

lvm_configure() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Configuring lvm backend in ${LXD_DIR}"

  lxc storage create "lxdtest-$(basename "${LXD_DIR}")" lvm volume.size=25MiB size=1GiB
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"
}

lvm_teardown() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Tearing down lvm backend in ${LXD_DIR}"
}
