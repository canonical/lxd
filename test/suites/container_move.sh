test_container_move() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxd_backend=$(storage_backend "$LXD_DIR")
  pool=$(lxc profile device get default root pool)
  pool2="test-pool"
  image="testimage"
  project="test-project"
  profile="test-profile"
  source_profile="source-profile"

  # Setup.
  lxc project create "${project}"
  if [ "${lxd_backend}" = "pure" ]; then
    configure_pure_pool "${pool2}"
  else
    lxc storage create "${pool2}" "${lxd_backend}"
  fi
  lxc profile create "${profile}" --project "${project}"
  lxc profile device add "${profile}" root disk pool="${pool2}" path=/ --project "${project}"

  # Move to different project with same profile (root disk device and profile are retained).
  lxc init "${image}" c1
  lxc move c1 --target-project "${project}"
  [ "$(lxc list --project "${project}" --format csv --columns n)" = "c1" ]                        # Verify new project.
  [ "$(lxc config device get c1 root pool --project "${project}")" = "${pool}" ]                  # Verify same pool (new local device).
  [ "$(lxc list --project "${project}" -c nP -f csv)" = "c1,default" ]                            # Verify profile is retained.
  lxc delete -f c1 --project "${project}"

  # Move to different project with no profiles (root disk device is retained).
  lxc init "${image}" c2
  lxc profile create "${source_profile}"
  lxc profile add c2 "${source_profile}"
  lxc snapshot c2 snap
  lxc move c2 --target-project "${project}" --no-profiles
  [ "$(lxc list --project "${project}" --format csv --columns n)" = "c2" ]                  # Verify new project.
  [ "$(lxc config device get c2 root pool --project "${project}")" = "${pool}" ]            # Verify same pool (new local device).
  [ "$(lxc list --project "${project}" -c nP -f csv)" = "c2," ]                             # Verify no profiles are applied.
  lxc delete -f c2 --project "${project}"
  lxc profile delete "${source_profile}"

  # Move to different project with new profiles (root disk device is retained).
  lxc init "${image}" c3
  lxc move c3 --target-project "${project}" --profile "${profile}"
  [ "$(lxc list --project "${project}" --format csv --columns n)" = "c3" ]       # Verify new project.
  [ "$(lxc config device get c3 root pool --project "${project}")" = "${pool}" ] # Verify same pool (new local device).
  lxc config show c3 -e --project "${project}" | grep -F -- "- ${profile}"       # Verify new profile.
  lxc delete -f c3 --project "${project}"

  # Move to different project with non-existing profile.
  lxc init "${image}" c4
  ! lxc move c4 --target-project "${project}" --profile invalid || false # Err: Profile not found in target project
  lxc delete -f c4

  # Move to different storage pool.
  lxc init "${image}" c5
  lxc move c5 --storage "${pool2}"
  [ "$(lxc list --format csv --columns n)" = "c5" ]        # Verify same project.
  [ "$(lxc config device get c5 root pool)" = "${pool2}" ] # Verify new pool.
  lxc delete -f c5

  # Move to different project and storage pool.
  lxc init "${image}" c6
  lxc move c6 --target-project "${project}" --storage "${pool2}"
  [ "$(lxc list --project "${project}" --format csv --columns n)" = "c6" ]        # Verify new project.
  [ "$(lxc config device get c6 root pool --project "${project}")" = "${pool2}" ] # Verify new pool.
  lxc delete -f c6 --project "${project}"

  # Move to different project and overwrite storage pool using device entry.
  lxc init "${image}" c7 --storage "${pool}" --no-profiles
  lxc move c7 --target-project "${project}" --device "root,pool=${pool2}"
  [ "$(lxc list --project "${project}" --format csv --columns n)" = "c7" ]        # Verify new project.
  [ "$(lxc config device get c7 root pool --project "${project}")" = "${pool2}" ] # Verify new pool.
  lxc delete -f c7 --project "${project}"

  # Move to different project and apply config entry.
  lxc init "${image}" c8
  lxc move c8 --target-project "${project}" --config user.test=success
  [ "$(lxc list --project "${project}" --format csv --columns n)" = "c8" ] # Verify new project.
  [ "$(lxc config get c8 user.test --project "${project}")" = "success" ]  # Verify new local config entry.
  lxc delete -f c8 --project "${project}"

  lxc profile delete "${profile}" --project "${project}"
  lxc storage delete "${pool2}"
  lxc project delete "${project}"
}
