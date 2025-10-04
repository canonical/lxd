ceph_setup() {
  local LXD_DIR="${1}"

  echo "==> Setting up CEPH backend in ${LXD_DIR}"
}

ceph_configure() {
  local LXD_DIR="${1}"
  local POOL_NAME="${2:-"lxdtest-${LXD_DIR##*/}"}" # Use the last part of the LXD_DIR as pool name

  echo "==> Configuring CEPH backend in ${LXD_DIR}"

  lxc storage create "${POOL_NAME}" ceph volume.size=25MiB ceph.osd.pg_num=8
  lxc profile device add default root disk path="/" pool="${POOL_NAME}"
}

ceph_teardown() {
  local LXD_DIR="${1}"

  echo "==> Tearing down CEPH backend in ${LXD_DIR}"
}
