test_check_deps() {
  ! ldd "$(command -v lxc)" | grep -q liblxc || false
}
