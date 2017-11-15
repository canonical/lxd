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

  # 1. Set a console log file but no ringbuffer.

  # Test the console log file requests.
  lxc start cons1

  # No console log file set so this should return an error.
  ! lxc console cons1 --show-log
  lxc stop --force cons1

  # Set a console log file but no ringbuffer.
  # shellcheck disable=SC2034
  CONSOLE_LOGFILE="$(mktemp -p "${LXD_DIR}" XXXXXXXXX)"
  lxc config set cons1 raw.lxc "lxc.console.logfile=${CONSOLE_LOGFILE}"
  lxc start cons1

  # Let the container come up. Two seconds should be fine.
  sleep 2

  # Make sure there's something in the console ringbuffer.
  echo 'some content' | lxc exec cons1 -- tee /dev/console

  # Console log file set so this should return without an error.
  lxc console cons1 --show-log

  lxc stop --force cons1

  # 2. Set a console ringbuffer but no log file.

  # remove logfile
  lxc config unset cons1 raw.lxc

  # set console ringbuffer
  lxc config set cons1 raw.lxc "lxc.console.logsize=auto"

  lxc start cons1

  # Let the container come up. Two seconds should be fine.
  sleep 2

  # Make sure there's something in the console ringbuffer.
  echo 'some content' | lxc exec cons1 -- tee /dev/console
  echo 'some more content' | lxc exec cons1 -- tee /dev/console

  # Retrieve the ringbuffer contents.
  lxc console cons1 --show-log

  # 3. Set a console ringbuffer and a log file.

  lxc stop --force cons1

  lxc config unset cons1 raw.lxc

  rm -f "${CONSOLE_LOGFILE}"
  printf "lxc.console.logsize=auto\nlxc.console.logfile=%s" "${CONSOLE_LOGFILE}" | lxc config set cons1 raw.lxc -

  lxc start cons1

  # Let the container come up. Two seconds should be fine.
  sleep 2

  # Make sure there's something in the console ringbuffer.
  echo 'Ringbuffer contents and log file contents must match' | lxc exec cons1 -- tee /dev/console

  # Retrieve the ringbuffer contents.
  RINGBUFFER_CONTENT=$(lxc console cons1 --show-log)
  # Strip prefix added by the client tool.
  RINGBUFFER_CONTENT="${RINGBUFFER_CONTENT#
Console log:

}"
  # Give kernel time to sync data to disk
  sleep 2
  LOGFILE_CONTENT=$(cat "${CONSOLE_LOGFILE}")

  [ "${RINGBUFFER_CONTENT}" = "${LOGFILE_CONTENT}" ]

  lxc delete --force cons1
}
