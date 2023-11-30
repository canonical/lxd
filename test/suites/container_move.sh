test_container_move() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxd_backend=$(storage_backend "$LXD_DIR")
  pool=$(lxc profile device get default root pool)
  pool2="test-pool"
  image="testimage"
  project="test-project"
  profile="test-profile"

  # Setup.
  lxc project create "${project}" --
  lxc storage create "${pool2}" "${lxd_backend}"
  lxc profile create "${profile}"
  lxc profile device add default root disk pool="${pool2}" path=/ --project "${project}"

  # Move project, verify root disk device is retained.
  lxc init "${image}" c1
  lxc move c1 --target-project "${project}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c1" ]         # Verify project.
  [ "$(lxc config device get c1 root pool --project ${project})" = "${pool}" ] # Verify pool is retained.
  lxc delete -f c1 --project "${project}"

  # Move to different storage pool.
  lxc init "${image}" c2
  lxc move c2 --storage "${pool2}"
  [ "$(lxc ls --format csv --columns n)" = "c2" ]          # Verify project.
  [ "$(lxc config device get c2 root pool)" = "${pool2}" ] # Verify pool.
  lxc delete -f c2

  # Move to different storage pool and project.
  lxc init "${image}" c3
  lxc move c3 --target-project "${project}" --storage "${pool2}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c3" ]          # Verify project.
  [ "$(lxc config device get c3 root pool --project ${project})" = "${pool2}" ] # Verify pool.
  lxc delete -f c3 --project "${project}"

  # Ensure profile is not retained.
  lxc init "${image}" c4 --profile default --profile "${profile}"
  ! lxc move c4 --target-project "${project}" # Err: Profile not found in target project
  lxc delete -f c4

  # Create matching profile in target project and ensure it is applied on move.
  lxc profile create "${profile}" --project "${project}"
  lxc profile set "${profile}" user.foo="test" --project "${project}"
  lxc init "${image}" c5 --profile default --profile "${profile}"
  lxc move c5 --target-project "${project}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c5" ] # Verify project.
  [ "$(lxc config get c5 user.foo -e --project ${project})" = "test" ] # Verify pool.
  lxc delete -f c5 --project "${project}"

  # Cleanup.
  lxc profile device remove default root --project "${project}"
  lxc profile delete "${profile}" --project "${project}"
  lxc profile delete "${profile}"
  lxc storage delete "${pool2}"
  lxc project delete "${project}"
}
