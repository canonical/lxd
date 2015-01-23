test_migration() {
  export LXD2_DIR=$(mktemp -d -p $(pwd))
  chmod 777 "${LXD2_DIR}"
  spawn_lxd 127.0.0.1:8444 "${LXD2_DIR}"
  lxd2_pid=$1

  (echo y; sleep 3; echo foo) | lxc remote add lxd2 127.0.0.1:8444
  lxc launch testimage migratee

  lxc move migratee lxd2:migratee
  lxc stop lxd2:migratee

  kill -9 lxd2_pid
  rm -rf "${LXD2_DIR}"
}
