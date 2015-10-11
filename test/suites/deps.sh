#!/bin/sh

test_check_deps() {
  ! ldd "$(which lxc)" | grep -q liblxc
}
