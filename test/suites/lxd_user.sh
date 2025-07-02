lxc_user() {
  local cmd
  cmd="$(unset -f lxc; command -v lxc)"
  sudo -u nobody -Es -- env LXD_CONF="${USER_TEMPDIR}" "${cmd}" "$@"
}

test_lxd_user() {
  mkdir "${TEST_DIR}/lxd-user"

  ( cd "${TEST_DIR}/lxd-user" && lxd-user ) &
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

  pool_name="$(lxc_user storage list -f csv | cut -d, -f1)"
  lxc_user init --empty c1 -s "${pool_name}"
  lxc_user storage volume create "${pool_name}" myvol

  lxc_user storage volume list "${pool_name}"
  lxc_user project list
  lxc_user list --fast

  # Cleanup
  lxc_user delete c1
  lxc_user storage volume delete "${pool_name}" myvol

  kill -9 "${USER_PID}"

  LXD_DIR="${bakLxdDir}"
}
