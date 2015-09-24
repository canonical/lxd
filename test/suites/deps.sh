test_check_deps() {
  bad=0
  ldd `which lxc` | grep -q liblxc && bad=1 || true
  if [ "${bad}" -eq 1 ]; then
    echo "this patchset adds a client dependency on liblxc"
  fi
}
