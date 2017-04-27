test_filemanip() {
  # Workaround for shellcheck getting confused by "cd"
  set -e
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  echo "test" > "${TEST_DIR}"/filemanip

  lxc launch testimage filemanip
  lxc exec filemanip -- ln -s /tmp/ /tmp/outside
  lxc file push "${TEST_DIR}"/filemanip filemanip/tmp/outside/

  [ ! -f /tmp/filemanip ]
  lxc exec filemanip -- ls /tmp/filemanip

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/containers/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  lxc delete filemanip -f

  if [ "$(storage_backend "$LXD_DIR")" != "lvm" ]; then
    lxc launch testimage idmap -c "raw.idmap=\"both 0 0\""
    [ "$(stat -c %u "${LXD_DIR}/containers/idmap/rootfs")" = "0" ]
    lxc delete idmap --force
  fi
}
