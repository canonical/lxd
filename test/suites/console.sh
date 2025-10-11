test_console() {
  ensure_import_testimage

  lxc launch testimage cons1

  # The VGA console is only available for VMs
  ! lxc console --type vga cons1 || false

  # Simulate console interactions with 'expect' and use 'tr' and 'grep' to
  # filter out leaked (control) chars. To debug, use 'expect -d'.
  console_output_file="$(mktemp -p "${TEST_DIR}" console.XXX)"
  cat << EOF | expect | tr -cd '[:print:]\n\t' | grep -vF '[6n' > "${console_output_file}"
set timeout 3
spawn lxc console cons1
sleep 0.1
expect "To detach from the console, press: <ctrl>+a q"
send "reset\r"
sleep 0.1
expect "\n\r"
send "\r"
sleep 0.1
expect "/ # *"
send "env\r"
sleep 0.1
expect "/ # *"
send "exit\r"
expect "Please press Enter to activate this console."
# ctrl+a q
send "\001q"
EOF

  if ! grep -xF 'TERM=vt102' "${console_output_file}"; then
    echo "Unexpected console output"
    cat --show-nonprinting "${console_output_file}"
    false
  fi
  rm "${console_output_file}"

  # Make sure there's something in the console ringbuffer.
  echo 'some content' | lxc exec cons1 -- tee /dev/console
  echo 'some more content' | lxc exec cons1 -- tee /dev/console

  # Retrieve the ringbuffer contents.
  lxc console cons1 --show-log | grep 'some content'

  lxc stop --force cons1

  # Retrieve on-disk representation of the console ringbuffer.
  lxc console cons1 --show-log | grep 'some more content'

  # Cleanup
  lxc delete cons1
}

test_snap_console_vm() {
  lxc launch ubuntu-minimal-daily:24.04 v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  waitInstanceReady v1

  # The VGA console is available for VMs
  echo "===> Check VGA console address"
  OUTPUT="$(timeout --foreground --signal KILL 0.1 lxc console --type vga v1 || true)"
  echo "${OUTPUT}" | grep -F "spice+unix:///"

  # 'lxc console --show-log' is only available for containers
  ! lxc console v1 --show-log || false

  # Cleanup
  lxc delete --force v1
}
