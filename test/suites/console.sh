test_console() {
  lxc_version=$(lxc info | grep "driver_version: " | cut -d' ' -f4)
  lxc_major=$(echo "${lxc_version}" | cut -d. -f1)

  if [ "${lxc_major}" -lt 3 ]; then
    echo "==> SKIP: The console ringbuffer require liblxc 3.0 or higher"
    return
  fi

  echo "==> API extension console"

  ensure_import_testimage

  lxc init testimage cons1

  lxc start cons1

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
