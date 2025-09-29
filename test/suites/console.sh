test_console() {
  echo "==> API extension console"

  ensure_import_testimage

  lxc launch testimage cons1

  # Make sure there's something in the console ringbuffer.
  echo 'some content' | lxc exec cons1 -- tee /dev/console
  echo 'some more content' | lxc exec cons1 -- tee /dev/console

  # Retrieve the ringbuffer contents.
  lxc console cons1 --show-log | grep 'some content'

  lxc stop --force cons1

  # Retrieve on-disk representation of the console ringbuffer.
  lxc console cons1 --show-log | grep 'some more content'

  lxc delete --force cons1
}
