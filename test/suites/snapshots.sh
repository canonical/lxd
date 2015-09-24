test_snapshots() {
  ensure_import_testimage
  ensure_has_localhost_remote 127.0.0.1:18443

  lxc init testimage foo

  lxc snapshot foo
  [ -d "${LXD_DIR}/snapshots/foo/snap0" ]

  lxc snapshot foo
  [ -d "${LXD_DIR}/snapshots/foo/snap1" ]

  lxc snapshot foo tester
  [ -d "${LXD_DIR}/snapshots/foo/tester" ]

  lxc copy foo/tester foosnap1
  [ -d "${LXD_DIR}/containers/foosnap1/rootfs" ]

  lxc delete foo/snap0
  [ ! -d "${LXD_DIR}/snapshots/foo/snap0" ]

  # no CLI for this, so we use the API directly (rename a snapshot)
  wait_for 127.0.0.1:18443 my_curl -X POST https://127.0.0.1:18443/1.0/containers/foo/snapshots/tester -d "{\"name\":\"tester2\"}"
  [ ! -d "${LXD_DIR}/snapshots/foo/tester" ]

  lxc move foo/tester2 foo/tester-two
  lxc delete foo/tester-two
  [ ! -d "${LXD_DIR}/snapshots/foo/tester-two" ]

  lxc snapshot foo namechange
  [ -d "${LXD_DIR}/snapshots/foo/namechange" ]
  lxc move foo foople
  [ ! -d "${LXD_DIR}/containers/foo" ]
  [ -d "${LXD_DIR}/containers/foople" ]
  [ -d "${LXD_DIR}/snapshots/foople/namechange" ]
  [ -d "${LXD_DIR}/snapshots/foople/namechange" ]

  lxc delete foople
  lxc delete foosnap1
  [ ! -d "${LXD_DIR}/containers/foople" ]
  [ ! -d "${LXD_DIR}/containers/foosnap1" ]
}

test_snap_restore() {
  # Skipping restore tests on Travis for now...
  if [ -n "${TRAVIS_PULL_REQUEST:-}" ]; then
    echo "SKIPPING"
    return
  fi

  ensure_import_testimage
  ensure_has_localhost_remote 127.0.0.1:18443

  lxc launch testimage bar

  ##########################################################
  # PREPARATION
  ##########################################################

  ## create some state we will check for when snapshot is restored

  ## prepare snap0
  echo snap0 > state
  lxc file push state bar/root/state
  lxc file push state bar/root/file_only_in_snap0
  lxc stop bar --force
  mkdir "${LXD_DIR}/containers/bar/rootfs/root/dir_only_in_snap0"
  cd "${LXD_DIR}/containers/bar/rootfs/root/"
  ln -s ./file_only_in_snap0 statelink
  cd -

  lxc snapshot bar snap0

  ## prepare snap1
  echo snap1 > state
  lxc start bar
  lxc file push state bar/root/state
  lxc file push state bar/root/file_only_in_snap1
  lxc stop bar --force
  cd "${LXD_DIR}/containers/bar/rootfs/root/"

  rmdir dir_only_in_snap0
  rm    file_only_in_snap0
  rm    statelink
  ln -s ./file_only_in_snap1 statelink
  mkdir dir_only_in_snap1
  cd -

  # Delete the state file we created to prevent leaking.
  rm state

  lxc config set bar limits.cpus 1

  lxc snapshot bar snap1

  ##########################################################

  restore_and_compare_fs snap0

  # Check container config has been restored (limits.cpus is unset)
  cpus=$(lxc config get bar limits.cpus)
  if [ "${cpus}" != "limits.cpus: " ]; then
   echo "==> config didn't match expected value after restore (${cpus})"
   false
  fi

  ##########################################################

  # test restore using full snapshot name
  restore_and_compare_fs snap1

  # Check config value in snapshot has been restored
  cpus=$(lxc config get bar limits.cpus)
  echo ${cpus}
  if [ "${cpus}" != "limits.cpus: 1" ]; then
   echo "==> config didn't match expected value after restore (${cpus})"
   false
  fi

  ##########################################################

  # Anything below this will not get run inside Travis-CI
  if [ -n "${TRAVIS_PULL_REQUEST:-}" ]; then
    lxc delete bar
    return
  fi

  # Start container and then restore snapshot to verify the running state after restore.
  lxc start bar

  restore_and_compare_fs snap0

  # check container is running after restore
  lxc list | grep bar | grep RUNNING

  lxc stop --force bar

  lxc delete bar
}

restore_and_compare_fs() {
  snap=${1}
  echo "\n ==> Restoring ${snap} \n"

  lxc restore bar ${snap}

  # Recursive diff of container FS
  echo "diff -r ${LXD_DIR}/containers/bar/rootfs ${LXD_DIR}/snapshots/bar/${snap}/rootfs"
  diff -r "${LXD_DIR}/containers/bar/rootfs" "${LXD_DIR}/snapshots/bar/${snap}/rootfs"
}
