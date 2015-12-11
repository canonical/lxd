zfs_setup() {
  local LXD_DIR
  LXD_DIR=$1

  if ! which zfs >/dev/null 2>&1; then
    echo "couldn't find zfs binary"; false
  fi

  truncate -s 100G "${LXD_DIR}/zfspool"
  # prefix lxdtest- here, as zfs pools must start with a letter, but tempdir
  # won't necessarily generate one that does.
  zpool create lxdtest-$(basename "${LXD_DIR}") "${LXD_DIR}/zfspool"
}

zfs_configure() {
  local LXD_DIR
  LXD_DIR=$1

  lxc config set storage.zfs_pool_name lxdtest-$(basename "${LXD_DIR}")
}

zfs_teardown() {
  local LXD_DIR
  LXD_DIR=$1

  zpool destroy -f lxdtest-$(basename "${LXD_DIR}")
}
