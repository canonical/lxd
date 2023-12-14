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
  lxc project create "${project}"
  lxc storage create "${pool2}" "${lxd_backend}"
  lxc profile create "${profile}" --project "${project}"
  lxc profile device add "${profile}" root disk pool="${pool2}" path=/ --project "${project}"

  # Move to different project with same profile (root disk device and profile are retained).
  lxc init "${image}" c1
  lxc move c1 --target-project "${project}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c1" ]                          # Verify new project.
  [ "$(lxc config device get c1 root pool --project ${project})" = "${pool}" ]                  # Verify same pool (new local device).
  [ "$(lxc ls --project "${project}" -c nP -f csv | awk -F, '/c1/ { print $2 }')" = "default" ] # Verify profile is retained.
  lxc delete -f c1 --project "${project}"

  # Move to different project with no profiles (root disk device is retained).
  lxc init "${image}" c2
  lxc move c2 --target-project "${project}" --no-profiles
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c2" ]                    # Verify new project.
  [ "$(lxc config device get c2 root pool --project ${project})" = "${pool}" ]            # Verify same pool (new local device).
  [ "$(lxc ls --project "${project}" -c nP -f csv | awk -F, '/c2/ { print $2 }')" = "" ]  # Verify no profiles are applied.
  lxc delete -f c2 --project "${project}"

  # Move to different project with new profiles (root disk device is retained).
  lxc init "${image}" c3
  lxc move c3 --target-project "${project}" --profile "${profile}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c3" ]         # Verify new project.
  [ "$(lxc config device get c3 root pool --project ${project})" = "${pool}" ] # Verify same pool (new local device).
  lxc config show c3 -e --project "${project}" | grep -- "- ${profile}"        # Verify new profile.
  lxc delete -f c3 --project "${project}"

  # Move to different project with non-existing profile.
  lxc init "${image}" c4
  ! lxc move c4 --target-project "${project}" --profile invalid # Err: Profile not found in target project
  lxc delete -f c4

  # Move to different storage pool.
  lxc init "${image}" c5
  lxc move c5 --storage "${pool2}"
  [ "$(lxc ls --format csv --columns n)" = "c5" ]          # Verify same project.
  [ "$(lxc config device get c5 root pool)" = "${pool2}" ] # Verify new pool.
  lxc delete -f c5

  # Move to different project and storage pool.
  lxc init "${image}" c6
  lxc move c6 --target-project "${project}" --storage "${pool2}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c6" ]          # Verify new project.
  [ "$(lxc config device get c6 root pool --project ${project})" = "${pool2}" ] # Verify new pool.
  lxc delete -f c6 --project "${project}"

  # Move to different project and overwrite storage pool using device entry.
  lxc init "${image}" c7 --storage "${pool}" --no-profiles
  lxc move c7 --target-project "${project}" --device "root,pool=${pool2}"
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c7" ]          # Verify new project.
  [ "$(lxc config device get c7 root pool --project ${project})" = "${pool2}" ] # Verify new pool.
  lxc delete -f c7 --project "${project}"

  # Move to different project and apply config entry.
  lxc init "${image}" c8
  lxc move c8 --target-project "${project}" --config user.test=success
  [ "$(lxc ls --project ${project} --format csv --columns n)" = "c8" ]  # Verify new project.
  [ "$(lxc config get c8 user.test --project ${project})" = "success" ] # Verify new local config entry.
  lxc delete -f c8 --project "${project}"

  lxc profile delete "${profile}" --project "${project}"
  lxc storage delete "${pool2}"
  lxc project delete "${project}"
}
