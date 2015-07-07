test_snapshots() {
  lxc init testimage foo

  lxc snapshot foo
  [ -d "$LXD_DIR/lxc/foo/snapshots/snap0" ]

  lxc snapshot foo
  [ -d "$LXD_DIR/lxc/foo/snapshots/snap1" ]

  lxc snapshot foo tester
  [ -d "$LXD_DIR/lxc/foo/snapshots/tester" ]

  lxc copy foo/tester foosnap1
  [ -d "$LXD_DIR/lxc/foosnap1/rootfs" ]

  lxc delete foo/snap0
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/snap0" ]

  # no CLI for this, so we use the API directly
  wait_for my_curl -X POST $BASEURL/1.0/containers/foo/snapshots/tester -d "{\"name\":\"tester2\"}"
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/tester" ]

  # no CLI for this, so we use the API directly
  wait_for my_curl -X DELETE $BASEURL/1.0/containers/foo/snapshots/tester2
  [ ! -d "$LXD_DIR/lxc/foo/snapshots/tester2" ]

  lxc delete foo
  lxc delete foosnap1
  [ ! -d "$LXD_DIR/lxc/foo" ]
}

test_snap_restore() {

  # Skipping restore tests on Travis for now...
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    echo "SKIPPING"
    return
  fi

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
  mkdir "$LXD_DIR/lxc/bar/rootfs/root/dir_only_in_snap0"
  cd "$LXD_DIR/lxc/bar/rootfs/root/"
  ln -s ./file_only_in_snap0 statelink
  cd -

  lxc snapshot bar snap0

  ## prepare snap1
  echo snap1 > state 
  lxc start bar
  lxc file push state bar/root/state
  lxc file push state bar/root/file_only_in_snap1
  lxc stop bar --force
  cd "$LXD_DIR/lxc/bar/rootfs/root/"

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
  if [ "$cpus" != "limits.cpus: " ]; then
   echo "==> config didn't match expected value after restore ($cpus)"
   false
  fi

  ##########################################################

  # test restore using full snapshot name
  restore_and_compare_fs snap1

  # Check config value in snapshot has been restored   
  cpus=$(lxc config get bar limits.cpus)
  echo $cpus
  if [ "$cpus" != "limits.cpus: 1" ]; then
   echo "==> config didn't match expected value after restore ($cpus)"
   false
  fi

  ##########################################################

  # Anything below this will not get run inside Travis-CI
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    lxc delete bar
    return
  fi

  # Start container and then restore snapshot to verify RUNNING state after restore.
  lxc start bar

  restore_and_compare_fs snap0

  # check container RUNNING after restore
  lxc list | grep bar | grep RUNNING

  lxc stop --force bar

  lxc delete bar
}

restore_and_compare_fs() {
  snap=$1
  echo "\n ==> Restoring $snap \n"

  lxc restore bar $1

  # Recursive diff of container FS
  echo "diff -r $LXD_DIR/lxc/bar/rootfs $LXD_DIR/lxc/bar/snapshots/$snap/rootfs"
  diff -r "$LXD_DIR/lxc/bar/rootfs" "$LXD_DIR/lxc/bar/snapshots/$snap/rootfs"
}
