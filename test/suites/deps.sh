test_check_deps() {
  echo "lxc binary must not be linked with liblxc"
  ! ldd "${_LXC}" | grep -F liblxc || false
}
