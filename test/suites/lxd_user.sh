lxc_user() {
  sudo -u nobody -Es -- env LXD_CONF="${USER_TEMPDIR}" "${_LXC}" "$@"
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

snap_lxc_user() {
    sudo -Hu testuser LXD_DIR=/var/snap/lxd/common/lxd-user lxc "${@}"
}

test_snap_lxd_user() {
  # Create testuser account
  if [ "$(id -u testuser)" != 5000 ]; then
    useradd --create-home testuser --groups lxd --uid 5000
  fi

  echo "==> Access the lxd-user daemon from a regular user"
  snap_lxc_user project list

  # Manually register the lxd-user daemon instance so that it can be cleaned up on failure
  local LXD_USER_DIR="/var/snap/lxd/common/lxd-user"
  pgrep -x lxd-user > "${LXD_USER_DIR}/lxd.pid"
  touch "${LXD_USER_DIR}/lxd.log"
  echo "${LXD_USER_DIR}" >> "${TEST_DIR}/daemons"
  # lxd-user uses the same storage pool as the system daemon
  storage_backend "${LXD_DIR}" > "${LXD_USER_DIR}/lxd.backend"

  echo "==> Check the user project was created"
  snap_lxc_user project list -f csv | grep '^user-5000.*,"User restricted project for ""testuser"" (5000)",'
  fingerprint="$(snap_lxc_user config trust list -f json | jq --exit-status --raw-output '.[] | select(.name == "lxd-user-5000") | .fingerprint')"
  snap_lxc_user query /1.0 | jq --exit-status '.auth_user_method == "tls" and .auth_user_name == "'"${fingerprint}"'"'

  # Cleanup
  lxc project delete user-5000
  userdel --remove --force testuser 2>/dev/null || true
}
