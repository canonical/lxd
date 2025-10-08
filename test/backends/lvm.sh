lvm_setup() {
  local LXD_DIR="${1}"

  echo "==> Setting up lvm backend in ${LXD_DIR}"
}

lvm_configure() {
  local LXD_DIR="${1}"
  local POOL_NAME="${2:-"lxdtest-${LXD_DIR##*/}"}" # Use the last part of the LXD_DIR as pool name

  echo "==> Configuring lvm backend in ${LXD_DIR}"

  lxc storage create "${POOL_NAME}" lvm volume.size=25MiB size=1GiB
  lxc profile device add default root disk path="/" pool="${POOL_NAME}"
}

lvm_teardown() {
  local LXD_DIR="${1}"

  echo "==> Tearing down lvm backend in ${LXD_DIR}"
}
