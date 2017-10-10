test_kernel_limits() {
  echo "==> API extension kernel_limits"

  ensure_import_testimage
  lxc init testimage limits
  # Set it to a limit < 65536 because older systemd's do not have my nofile
  # limit patch.
  lxc config set limits limits.kernel.nofile 3000
  lxc start limits
  pid=$(lxc info limits | grep ^Pid | awk '{print $2}')
  soft=$(grep ^"Max open files" /proc/"${pid}"/limits | awk '{print $4}')
  hard=$(grep ^"Max open files" /proc/"${pid}"/limits | awk '{print $5}')

  lxc delete --force limits 

  [ "${soft}" = "3000" ] && [ "${hard}" = "3000" ]
}
