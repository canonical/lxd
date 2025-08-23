test_container_syscall_interception() {
  ensure_import_testimage

  lxc launch testimage c1 -c limits.memory=123MiB
  lxc file push --quiet "$(command -v sysinfo)" c1/root/sysinfo
  lxc exec c1 -- /root/sysinfo
  ! lxc exec c1 -- /root/sysinfo | grep "Totalram:128974848 " || false
  lxc stop -f c1
  lxc config set c1 security.syscalls.intercept.sysinfo=true
  lxc start c1
  lxc exec c1 -- /root/sysinfo
  lxc exec c1 -- /root/sysinfo | grep "Totalram:128974848 "
  lxc delete -f c1
}
