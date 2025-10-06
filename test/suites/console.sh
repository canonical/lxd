test_console() {
  echo "==> API extension console"

  ensure_import_testimage

  lxc launch testimage cons1

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
