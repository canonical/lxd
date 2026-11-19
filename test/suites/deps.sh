test_check_deps() {
  ! ldd "$(unset -f lxc; command -v lxc)" | grep -q liblxc || false
}
