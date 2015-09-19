test_filemanip() {
  if [ -n "${TRAVIS_PULL_REQUEST:-}" ]; then
    return
  fi

  ensure_import_testimage

  # Check that symlinks are interpreted relative to the container's root
  mkdir -p /tmp/outside

  lxc launch testimage filemanip
  lxc exec filemanip -- mkdir /tmp/outside
  lxc exec filemanip -- ln -s /tmp/outside /tmp/inside
  lxc file push main.sh filemanip/tmp/inside/

  [ ! -f /tmp/outside/main.sh ]
  [ -f ${LXD_DIR}/containers/filemanip/rootfs/tmp/outside/main.sh ]

  rm -rf /tmp/outside
  lxc delete filemanip
}
