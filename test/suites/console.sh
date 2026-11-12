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

  # The shared console helper works for containers too.
  # shellcheck disable=SC2016 # The arithmetic is expanded by the container shell.
  instance_console_run cons1 'echo cons-$((40+2))' "cons-42"

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

test_console_vm() {
  lxc launch --empty v1 --vm -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"

  # 'lxc console --show-log' is only available for containers
  ! lxc console v1 --show-log || false

  # The VGA console is available for VMs
  echo "===> Check VGA console address"
  OUTPUT="$(mktemp -p "${TEST_DIR}" console_output.XXX)"
  lxc console --type vga v1 > "${OUTPUT}" &
  CONSOLE_PID=$!

  # Wait for the console output to contain the SPICE socket address
  for _ in 1 2 3; do
    grep -F "spice+unix:///" < "${OUTPUT}" && break
    sleep 0.1
  done

  SPICE_UNIX_SOCKET="$(sed -n '/spice+unix/ s|^\s\+spice+unix://|| p' < "${OUTPUT}")"
  [ -S "${SPICE_UNIX_SOCKET}" ]

  echo "===> Test SPICE socket connectivity"
  # This will cause the `lxc console --type vga` command to exit once connected
  nc -zvU "${SPICE_UNIX_SOCKET}"

  echo "===> Verify console command has exited and cleaned up the socket"
  wait "${CONSOLE_PID}" || true
  ! [ -e "${SPICE_UNIX_SOCKET}" ] || false

  # Cleanup
  lxc delete --force v1
  rm "${OUTPUT}"
}

test_console_vm_login() {
  ensure_import_ubuntu_vm_image

  lxc launch ubuntu-vm v1 --vm -c limits.memory=384MiB -d "${SMALL_VM_ROOT_DISK}"
  waitInstanceReady v1

  # Set the password that instance_console_run logs in with. root exists from the start, unlike the
  # default user that cloud-init creates later in the boot.
  echo "root:lxdtest" | lxc exec v1 -- chpasswd

  # The getty on the serial console only starts late in the boot, once the network wait is over.
  # shellcheck disable=SC2016 # The arithmetic is expanded by the guest shell.
  instance_console_run v1 'echo vm-$((40+2))' "vm-42" 300 true

  # Cleanup
  lxc delete --force v1
}

test_console_microvm() {
  if ! _microvm_check_prerequisites; then
    return 0
  fi

  ensure_import_testimage

  lxc launch testimage m1 --microvm -c limits.memory=256MiB

  # The VGA console is only available for VMs
  ! lxc console --type vga m1 || false

  # 'lxc console --show-log' is only available for containers
  ! lxc console m1 --show-log || false

  # shellcheck disable=SC2016 # The arithmetic is expanded by the guest shell.
  instance_console_run m1 'echo booted-$((40+2))' "booted-42"

  # Cleanup
  lxc delete --force m1
}
