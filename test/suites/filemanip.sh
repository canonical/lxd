#!/bin/sh

test_filemanip() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc launch testimage filemanip
  lxc exec filemanip -- ln -s /tmp/ /tmp/outside
  lxc file push main.sh filemanip/tmp/outside/

  [ ! -f /tmp/main.sh ]
  lxc exec filemanip -- ls /tmp/main.sh

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${LXD_ADDR}/1.0/containers/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  lxc delete filemanip -f
}
