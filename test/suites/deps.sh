test_check_deps() {
  echo "lxc binary must not be linked with liblxc"
  ! ldd "$(command -v lxc)" | grep -F liblxc || false
}
