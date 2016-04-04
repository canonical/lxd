#!/bin/sh

test_filemanip() {
  ensure_import_testimage

  lxc launch testimage filemanip
  lxc exec filemanip -- ln -s /tmp/ /tmp/outside
  lxc file push main.sh filemanip/tmp/outside/

  [ ! -f /tmp/main.sh ]
  lxc exec filemanip -- ls /tmp/main.sh

  lxc delete filemanip -f
}
