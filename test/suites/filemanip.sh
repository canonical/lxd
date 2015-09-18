test_filemanip() {
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
    if [ -e "$LXD_TEST_IMAGE" ]; then
      lxc image import $LXD_TEST_IMAGE --alias testimage
    else
      ../scripts/lxd-images import busybox --alias testimage
    fi
  fi

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
