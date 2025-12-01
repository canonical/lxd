test_kernel_limits() {
  echo "==> API extension kernel_limits"

  ensure_import_testimage
  lxc launch testimage limits --config limits.kernel.nofile=3000
  # Set it to a limit < 65536 because older systemd's do not have any nofile
  # limit patch.
  pid="$(lxc list -f csv -c p limits)"

  # Extract soft and hard limits from /proc/<pid>/limits
  limits="$(awk '/^Max open files/ {print $4 " " $5}' "/proc/${pid}/limits")"

  lxc delete --force limits

  [ "${limits}" = "3000 3000" ]
}
