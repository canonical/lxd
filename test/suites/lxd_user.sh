test_lxd_user() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  mkdir "${TEST_DIR}/lxd-user"
  cd "${TEST_DIR}/lxd-user" || return

  lxd-user &
  USER_PID="$!"
  while :; do
    [ -S "${TEST_DIR}/lxd-user/unix.socket" ] && break
    sleep 0.5
  done

  chgrp "nogroup" "${TEST_DIR}/lxd-user/unix.socket"
  chmod 0660 "${TEST_DIR}/lxd-user/unix.socket"

  USER_TEMPDIR="${TEST_DIR}/user"
  mkdir "${USER_TEMPDIR}"
  chown nobody:nogroup "${USER_TEMPDIR}"

  bakLxdDir="${LXD_DIR}"
  LXD_DIR="${TEST_DIR}/lxd-user"

  cmd=$(unset -f lxc; command -v lxc)
  sudo -u nobody -Es -- env LXD_CONF="${USER_TEMPDIR}" "${cmd}" project list

  kill -9 "${USER_PID}"

  LXD_DIR="${bakLxdDir}"
}
