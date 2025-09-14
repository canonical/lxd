# Use the default project.
test_projects_default() {
  # The default project is used by the default profile
  lxc project show default | grep -xF -- "- /1.0/profiles/default"

  # Containers and images are assigned to the default project
  ensure_import_testimage
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc project show default | grep -xF -- "- /1.0/profiles/default"
  lxc project show default | grep -F -- "- /1.0/images/"
  lxc delete c1
  lxc image delete testimage
}

# CRUD operations on project.
test_projects_crud() {
  # Create a project
  lxc project create foo

  # All features are enabled by default
  lxc project show foo | grep -F 'features.images: "true"'
  [ "$(lxc project get foo "features.profiles")" = "true" ]

  # Set a limit
  lxc project set foo limits.containers 10
  lxc project show foo | grep -F 'limits.containers: "10"'
  [ "$(lxc project get foo limits.containers)" = "10" ]

  # Trying to create a project with the same name fails
  ! lxc project create foo || false

  # Trying to create a project containing an underscore fails
  ! lxc project create foo_banned || false

  # Rename the project to a banned name fails
  ! lxc project rename foo bar_banned || false

  # Rename the project and check it occurs
  lxc project rename foo bar
  lxc project show bar

  # Edit the project
  lxc project show bar| sed 's/^description:.*/description: "Bar project"/' | lxc project edit bar
  lxc project show bar | grep -xF "description: Bar project"

  # Edit the project config via PATCH. Existing key/value pairs should remain or be updated.
  lxc query -X PATCH -d '{\"config\" : {\"limits.memory\":\"5GiB\",\"features.images\":\"false\"}}' /1.0/projects/bar
  lxc project show bar | grep -F 'limits.memory: 5GiB'
  lxc project show bar | grep -F 'features.images: "false"'
  lxc project show bar | grep -F 'features.profiles: "true"'
  lxc project show bar | grep -F 'limits.containers: "10"'

  # Create a second project
  lxc project create foo

  # Trying to rename a project using an existing name fails
  ! lxc project rename bar foo || false

  lxc project switch foo

  # Turning off the profiles feature makes the project see the default profile
  # from the default project.
  lxc project set foo features.profiles false
  lxc profile show default | grep -xF 'description: Default LXD profile'

  # Turning on the profiles feature creates a project-specific default
  # profile.
  lxc project set foo features.profiles true
  lxc profile show default | grep -xF 'description: Default LXD profile for project foo'

  # Invalid config values are rejected.
  ! lxc project set foo garbage xxx || false

  lxc project switch default

  # Delete the projects
  lxc project delete foo
  lxc project delete bar

  # We're back to the default project
  lxc project list | grep -F "default (current)"
}

# Use containers in a project.
test_projects_containers() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  deps/import-busybox --project foo --alias testimage
  fingerprint="$(lxc image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a root device to the default profile of the project
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  # Create a container in the project
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"

  # The container is listed when using this project
  lxc list -c n | grep -wF c1
  lxc info c1 | grep -xF "Name: c1"

  # The container's volume is listed too.
  lxc storage volume list "${pool}" | grep -wF container | grep -wF c1

  # For backends with optimized storage, we can see the image volume inside the
  # project.
  driver="$(storage_backend "$LXD_DIR")"
  if [ "${driver}" != "dir" ]; then
      lxc storage volume list "${pool}" | grep -wF image | grep -F "${fingerprint}"
  fi

  # Start the container
  lxc start c1
  [ "$(lxc list -f csv -c ns c1)" = "c1,RUNNING" ]
  [ "$(echo "abc" | lxc exec c1 cat)" = "abc" ]

  # The container can't be managed when using the default project
  lxc project switch default
  ! lxc list -c n | grep -wF c1 || false
  ! lxc info c1 || false
  ! lxc delete c1 || false
  ! lxc storage volume list "${pool}" | grep -wF container | grep -wF c1 || false

  # Trying to delete a project which is in use fails
  ! lxc project delete foo || false

  # Trying to change features of a project which is in use fails
  ! lxc project show foo| sed 's/features.profiles:.*/features.profiles: "false"/' | lxc project edit foo || false
  ! lxc project set foo "features.profiles" "false" || false
  lxc project show foo | grep -F 'features.profiles: "true"'

  # Create a container with the same name in the default project
  ensure_import_testimage
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc start c1
  [ "$(lxc list -f csv -c ns c1)" = "c1,RUNNING" ]
  lxc stop --force c1

  # Delete the container
  lxc project switch foo

  lxc delete --force c1
  lxc image delete testimage

  # Delete the project
  lxc project delete foo

  # The container in the default project can still be used
  lxc start c1
  [ "$(lxc list -f csv -c ns c1)" = "c1,RUNNING" ]
  lxc delete --force c1
}

# Copy/move between projects
test_projects_copy() {
  ensure_import_testimage

  # Create a couple of projects
  lxc project create foo -c features.profiles=false -c features.images=false
  lxc project create bar -c features.profiles=false -c features.images=false

  # Create a container in the project
  lxc --project foo init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc --project foo copy c1 c1 --target-project bar
  lxc --project bar start c1
  lxc --project bar delete c1 -f

  lxc --project foo snapshot c1
  lxc --project foo snapshot c1
  lxc --project foo snapshot c1

  lxc --project foo copy c1/snap0 c1 --target-project bar
  lxc --project bar start c1
  lxc --project bar delete c1 -f

  lxc --project foo copy c1 c1 --target-project bar
  lxc --project foo start c1
  lxc --project bar start c1

  lxc --project foo delete c1 -f
  lxc --project bar stop c1 -f
  lxc --project bar move c1 c1 --target-project foo
  lxc --project foo start c1
  lxc --project foo delete c1 -f

  # Move storage volume between projects
  pool="lxdtest-$(basename "${LXD_DIR}")"

  lxc --project foo storage volume create "${pool}" vol1
  lxc --project foo --target-project bar storage volume move "${pool}"/vol1 "${pool}"/vol1

  # Clean things up
  lxc --project bar storage volume delete "${pool}" vol1
  lxc project delete foo
  lxc project delete bar
}

# Use snapshots in a project.
test_projects_snapshots() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create a container in the project
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"

  # Create, rename, restore and delete a snapshot
  lxc snapshot c1
  lxc info c1 | grep -wF snap0
  lxc config show c1/snap0 | grep -wF BusyBox
  lxc rename c1/snap0 c1/foo
  lxc restore c1 foo
  lxc delete c1/foo

  # Test copies
  lxc snapshot c1
  lxc snapshot c1
  lxc copy c1 c2
  lxc delete c2

  # Create a snapshot in this project and another one in the default project
  lxc snapshot c1

  lxc project switch default
  ensure_import_testimage
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"
  lxc snapshot c1
  lxc delete c1

  # Switch back to the project
  lxc project switch foo

  # Delete the container
  lxc delete c1

  # Delete the project
  lxc image delete testimage
  lxc project delete foo
}

# Use backups in a project.
test_projects_backups() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create a container in the project
  lxc init --empty c1 -d "${SMALL_ROOT_DISK}"

  mkdir "${LXD_DIR}/non-optimized"

  # Create a backup.
  lxc export c1 "${LXD_DIR}/c1.tar.gz"
  tar -xzf "${LXD_DIR}/c1.tar.gz" -C "${LXD_DIR}/non-optimized"

  # Check tarball content
  [ -f "${LXD_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${LXD_DIR}/non-optimized/backup/container" ]

  # Delete the container
  lxc delete c1

  # Import the backup.
  lxc import "${LXD_DIR}/c1.tar.gz"
  lxc info c1
  lxc delete c1

  # Delete the project
  rm -rf "${LXD_DIR}/non-optimized/"
  lxc project delete foo
}

# Use private profiles in a project.
test_projects_profiles() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # List profiles
  lxc profile list | grep -wF 'default'
  lxc profile show default | grep -xF 'description: Default LXD profile for project foo'

  # Create a profile in this project
  lxc profile create p1
  lxc profile list | grep -wF 'p1'

  # Set a config key on this profile
  lxc profile set p1 user.x y
  [ "$(lxc profile get p1 user.x)" = "y" ]

  # The profile is not visible in the default project
  lxc project switch default
  ! lxc profile list | grep -wF 'p1' || false

  # The profile is visible in the default project when --all-projects is used
  lxc profile list --all-projects | grep -wF 'p1'

  # A profile with the same name can be created in the default project
  lxc profile create p1

  # The same key can have a different value
  lxc profile set p1 user.x z
  [ "$(lxc profile get p1 user.x)" = "z" ]

  # Switch back to the project
  lxc project switch foo

  # The profile has still the original config
  [ "$(lxc profile get p1 user.x)" = "y" ]

  # Delete the profile from the project
  lxc profile delete p1

  # Delete the project
  lxc project delete foo

  # Delete the profile from the default project
  lxc profile delete p1

  # Try project copy
  lxc project create foo
  lxc profile set --project default default user.x z
  lxc profile copy --project default --target-project foo default bar
  # copy to an existing profile without --refresh should fail
  ! lxc profile copy --project default --target-project foo default bar || false
  lxc profile copy --project default --target-project foo default bar --refresh
  [ "$(lxc profile get --project foo bar user.x)" = "z" ]
  lxc profile copy --project default --target-project foo default bar-non-existent --refresh
  lxc profile delete bar --project foo
  lxc profile delete bar-non-existent --project foo
  lxc project delete foo
}

# Use global profiles in a project.
test_projects_profiles_default() {
  # Create a new project, without the features.profiles config.
  lxc project create -c features.profiles=false foo
  lxc project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(lxc image list -c f --format json | jq .[0].fingerprint)"

  # Create a container
  lxc init "${fingerprint}" c1 -d "${SMALL_ROOT_DISK}"

  # Switch back the default project
  lxc project switch default

  # Try updating the default profile
  lxc profile set default user.foo bar
  lxc profile unset default user.foo

  # Create a container in the default project as well.
  ensure_import_testimage
  lxc init testimage c1 -d "${SMALL_ROOT_DISK}"

  # If we look at the global profile we see that it's being used by both the
  # container in the above project and the one we just created.
  lxc profile show default | grep -E -q '^- /1.0/instances/c1$'
  lxc profile show default | grep -E -q '^- /1.0/instances/c1\?project=foo$'

  lxc delete c1

  lxc project switch foo

  # Delete the project
  lxc delete c1
  lxc image delete "${fingerprint}"
  lxc project delete foo

  # Create another project using --storage and --network flags
  lxc project create bar --storage default --network lxdbr0

  # Ensure default profile properly set up
  lxc profile show default --project bar | grep -E -q "network: lxdbr0"
  lxc profile show default --project bar | grep -E -q "pool: default"

  # Delete project
  lxc project delete bar

  # Ensure failure when --network and features.networks=true used together
  ! lxc project create bar --network lxdbr0 -c features.networks=true || false
}

# Use private images in a project.
test_projects_images() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(lxc image list -c f --format json | jq .[0].fingerprint)"

  # The imported image is not visible in the default project.
  lxc project switch default
  ! lxc image list | grep -F "${fingerprint}" || false

  # Switch back to the project and clean it up.
  lxc project switch foo
  lxc image delete "${fingerprint}"

  # Now Import an image into the project assigning it an alias
  deps/import-busybox --project foo --alias foo-image

  # The image alias shows up in the project
  lxc image list | grep -wF foo-image

  # However the image alias is not visible in the default project.
  lxc project switch default
  ! lxc image list | grep -wF foo-project || false

  # Let's import the same image in the default project
  ensure_import_testimage

  # Switch back to the project.
  lxc project switch foo

  # The image alias from the default project is not visible here
  ! lxc image list | grep -wF testimage || false

  # Rename the image alias in the project using the same it has in the default
  # one.
  lxc image alias rename foo-image testimage

  # Create another alias for the image
  lxc image alias create egg-image "${fingerprint}"

  # Delete the old alias
  lxc image alias delete testimage

  # Delete the project and image altogether
  lxc image delete egg-image
  lxc project delete foo

  # We automatically switched to the default project, which still has the alias
  lxc image list | grep -wF testimage
}

# Use global images in a project.
test_projects_images_default() {
  # Make sure that there's an image in the default project
  ensure_import_testimage

  # Create a new project, without the features.images config.
  lxc project create foo --config features.images=false
  lxc project switch foo

  # Create another project, without the features.images config.
  lxc project create bar --config features.images=false

  # The project can see images from the default project
  lxc image list | grep -wF testimage

  # The image from the default project has correct profile assigned
  fingerprint="$(lxc image list --format json | jq -r .[0].fingerprint)"
  [ "$(lxc query "/1.0/images/${fingerprint}?project=foo" | jq -r ".profiles[0]")" = "default" ]

  # The project can delete images in the default project
  lxc image delete testimage

  # Images imported into the project show up in the default project
  deps/import-busybox --project foo --alias foo-image
  lxc image list | grep -wF foo-image
  lxc project switch default
  lxc image list | grep -wF foo-image

  # Correct profile assigned to images from another project
  fingerprint="$(lxc image list --format json | jq -r '.[] | select(.aliases[0].name == "foo-image") | .fingerprint')"
  [ "$(lxc query "/1.0/images/${fingerprint}?project=bar" | jq -r ".profiles[0]")" = "default" ]

  lxc image delete foo-image

  lxc project delete bar
  lxc project delete foo
}

# Interaction between projects and storage pools.
test_projects_storage() {
  pool="lxdtest-$(basename "${LXD_DIR}")"

  lxc storage volume create "${pool}" vol

  lxc project create foo -c features.storage.volumes=false
  lxc project switch foo

  lxc storage volume list "${pool}" | grep -wF custom | grep -wF vol

  lxc storage volume delete "${pool}" vol

  lxc project switch default

  ! lxc storage volume list "${pool}" | grep -wF custom | grep -F vol || false

  lxc project set foo features.storage.volumes=true
  lxc storage volume create "${pool}" vol
  lxc project switch foo
  ! lxc storage volume list "${pool}" | grep -wF custom | grep -wF vol || false

  lxc storage volume create "${pool}" vol
  lxc storage volume delete "${pool}" vol

  lxc storage volume create "${pool}" vol2
  lxc project switch default
  ! lxc storage volume list "${pool}" | grep -wF custom | grep -wF vol2 || false

  lxc project switch foo
  lxc storage volume delete "${pool}" vol2

  lxc project switch default
  lxc storage volume delete "${pool}" vol
  lxc project delete foo
}

# Interaction between projects and networks.
test_projects_network() {
  # Standard bridge with random subnet and a bunch of options
  network="lxdt$$"
  lxc network create "${network}" ipv4.address=none ipv6.address=none

  lxc project create foo
  lxc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create a container in the project
  lxc init -n "${network}" testimage c1 -d "${SMALL_ROOT_DISK}"

  lxc network show "${network}" | grep -xF -- "- /1.0/instances/c1?project=foo"

  # Delete the container
  lxc delete c1

  # Delete the project
  lxc image delete testimage
  lxc project delete foo

  lxc network delete "${network}"
}

# Set resource limits on projects.
test_projects_limits() {
  # Create a project
  lxc project create p1

  # Instance limits validation
  ! lxc project set p1 limits.containers xxx || false
  ! lxc project set p1 limits.virtual-machines -1 || false

  lxc project switch p1

  # Add a root device to the default profile of the project and import an image.
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  # Test per-pool limits.
  lxc storage create limit1 dir
  lxc storage create limit2 dir

  lxc project set p1 limits.disk=50MiB
  lxc project set p1 limits.disk.pool.limit1=0
  lxc project set p1 limits.disk.pool.limit2=0

  ! lxc storage list | grep -F limit1 || false
  ! lxc storage list | grep -F limit2 || false

  lxc storage volume create "${pool}" foo size=10MiB
  ! lxc storage volume create "${pool}" bar size=50MiB || false
  lxc storage volume delete "${pool}" foo

  ! lxc storage volume create limit1 foo size=10GiB || false
  ! lxc storage volume create limit2 foo size=10GiB || false

  lxc project set p1 limits.disk.pool.limit1=10MiB
  lxc project set p1 limits.disk.pool.limit2=10MiB
  lxc storage volume create limit1 foo size=10MiB
  ! lxc storage volume create limit1 bar size=10MiB || false
  lxc storage volume create limit2 foo size=10MiB
  ! lxc storage volume create limit2 bar size=10MiB || false

  ! lxc storage volume create "${pool}" foo size=40MiB || false
  lxc storage volume delete limit1 foo
  lxc storage volume delete limit2 foo
  lxc storage volume create "${pool}" foo size=40MiB

  lxc storage volume delete "${pool}" foo
  lxc project unset p1 limits.disk.pool.limit1
  lxc project unset p1 limits.disk.pool.limit2
  lxc project unset p1 limits.disk
  lxc storage delete limit1
  lxc storage delete limit2

  # Create a couple of containers in the project.
  lxc init --empty c1
  lxc init --empty c2

  # Can't set the containers limit below the current count.
  ! lxc project set p1 limits.containers 1 || false

  # Can't create containers anymore after the limit is reached.
  lxc project set p1 limits.containers 2
  ! lxc init --empty c3 || false

  # Can't set the project's memory limit to a percentage value.
  ! lxc project set p1 limits.memory 10% || false

  # Can't set the project's memory limit because not all instances have
  # limits.memory defined.
  ! lxc project set p1 limits.memory 10GiB || false

  # Set limits.memory on the default profile.
  lxc profile set default limits.memory 1GiB

  # Can't set the memory limit below the current total usage.
  ! lxc project set p1 limits.memory 1GiB || false

  # Configure a valid project memory limit.
  lxc project set p1 limits.memory 3GiB

  # Validate that snapshots don't fail with limits.
  lxc snapshot c2
  lxc restore c2 snap0

  lxc delete c2

  # Create a new profile which does not define "limits.memory".
  lxc profile create unrestricted
  lxc profile device add unrestricted root disk path="/" pool="${pool}"

  # Can't create a new container without defining "limits.memory"
  ! lxc init --empty c2 -p unrestricted || false

  # Can't create a new container if "limits.memory" is too high
  ! lxc init --empty c2 -p unrestricted -c limits.memory=4GiB || false

  # Can't create a new container if "limits.memory" is a percentage
  ! lxc init --empty c2 -p unrestricted -c limits.memory=10% || false

  # No error occurs if we define "limits.memory" and stay within the limits.
  lxc init --empty c2 -p unrestricted -c limits.memory=1GiB

  # Can't change the container's "limits.memory" if it would overflow the limit.
  ! lxc config set c2 limits.memory=4GiB || false

  # Can't unset the instance's "limits.memory".
  ! lxc config unset c2 limits.memory || false

  # Can't unset the default profile's "limits.memory", as it would leave c1
  # without an effective "limits.memory".
  ! lxc profile unset default limits.memory || false

  # Can't check the default profile's "limits.memory" to a value that would
  # violate project's limits.
  ! lxc profile set default limits.memory=4GiB || false

  # Can't change limits.memory to a percentage.
  ! lxc profile set default limits.memory=10% || false
  ! lxc config set c2 limits.memory=10% || false

  # It's possible to change both a profile and an instance memory limit, if they
  # don't break the project's aggregate allowance.
  lxc profile set default limits.memory=2GiB
  lxc config set c2 limits.memory=512MiB

  # Can't set the project's processes limit because no instance has
  # limits.processes defined.
  ! lxc project set p1 limits.processes 100 || false

  # Set processes limits on the default profile and on c2.
  lxc profile set default limits.processes=50
  lxc config set c2 limits.processes=50

  # Can't set the project's processes limit if it's below the current total.
  ! lxc project set p1 limits.processes 75 || false

  # Set the project's processes limit.
  lxc project set p1 limits.processes 150

  # Changing profile and instance processes limits within the aggregate
  # project's limit is fine.
  lxc profile set default limits.processes=75
  lxc config set c2 limits.processes=75

  # Changing profile and instance processes limits above the aggregate project's
  # limit is not possible.
  ! lxc profile set default limits.processes=80 || false
  ! lxc config set c2 limits.processes=80 || false

  # Changing the project's processes limit below the current aggregate amount is
  # not possible.
  ! lxc project set p1 limits.processes 125 || false

  # Set a cpu limit on the default profile and on the instance, with c2
  # using CPU pinning.
  lxc profile set default limits.cpu=2
  lxc config set c2 limits.cpu=0,1

  # It's not possible to set the project's cpu limit since c2 is using CPU
  # pinning.
  ! lxc project set p1 limits.cpu 4 || false

  # Change c2's from cpu pinning to a regular cpu count limit.
  lxc config set c2 limits.cpu=2

  # Can't set the project's cpu limit below the current aggregate count.
  ! lxc project set p1 limits.cpu 3 || false

  # Set the project's cpu limit
  lxc project set p1 limits.cpu 4

  # Can't update the project's cpu limit below the current aggregate count.
  ! lxc project set p1 limits.cpu 3 || false

  # Changing profile and instance cpu limits above the aggregate project's
  # limit is not possible.
  ! lxc profile set default limits.cpu=3 || false
  ! lxc config set c2 limits.cpu=3 || false

  # CPU limits can be updated if they stay within limits.
  lxc project set p1 limits.cpu 7
  lxc profile set default limits.cpu=3
  lxc config set c2 limits.cpu=3

  # Can't set the project's disk limit because not all instances have
  # the "size" config defined on the root device.
  ! lxc project set p1 limits.disk 1GiB || false

  # Set a disk limit on the default profile and also on instance c2
  lxc profile device set default root size=100MiB
  lxc config device add c2 root disk path="/" pool="${pool}" size=50MiB

  if [ "${LXD_BACKEND}" = "lvm" ]; then
    # Can't set the project's disk limit because not all volumes have
    # the "size" config defined.
    pool1="lxdtest1-$(basename "${LXD_DIR}")"
    lxc storage create "${pool1}" lvm size=1GiB
    lxc storage volume create "${pool1}" v1
    ! lxc project set p1 limits.disk 1GiB || false
    lxc storage volume delete "${pool1}" v1
    lxc storage delete "${pool1}"
  fi

  # Create a custom volume without any size property defined.
  lxc storage volume create "${pool}" v1

  # Set a size on the custom volume.
  lxc storage volume set "${pool}" v1 size 50MiB

  # Can't set the project's disk limit below the current aggregate count.
  ! lxc project set p1 limits.disk 190MiB || false

  # Set the project's disk limit
  lxc project set p1 limits.disk 250MiB

  # Can't update the project's disk limit below the current aggregate count.
  ! lxc project set p1 limits.disk 190MiB || false

  # Changing profile or instance root device size or volume size above the
  # aggregate project's limit is not possible.
  ! lxc profile device set default root size=160MiB || false
  ! lxc config device set c2 root size 110MiB || false
  ! lxc storage volume set "${pool}" v1 size 110MiB || false

  # Can't create a custom volume without specifying a size.
  ! lxc storage volume create "${pool}" v2 || false

  # Disk limits can be updated if they stay within limits.
  lxc project set p1 limits.disk 204900KiB
  lxc profile device set default root size=90MiB
  lxc config device set c2 root size 60MiB

  # Can't upload an image if that would exceed the current quota.
  ! deps/import-busybox --project p1 --template start --alias otherimage || false

  # Can't export publish an instance as image if that would exceed the current
  # quota.
  ! lxc publish c1 --alias=c1image || false

  # Run the following part of the test only against the dir or zfs backend,
  # since it on other backends it requires resize the rootfs to a value which is
  # too small for resize2fs.
  if [ "${LXD_BACKEND}" = "dir" ] || [ "${LXD_BACKEND}" = "zfs" ]; then
    # Add a remote LXD to be used as image server.
    local LXD_REMOTE_DIR
    LXD_REMOTE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

    # Switch to default project to spawn new LXD server, and then switch back to p1.
    lxc project switch default
    spawn_lxd "${LXD_REMOTE_DIR}" true
    lxc project switch p1

    LXD_REMOTE_ADDR=$(< "${LXD_REMOTE_DIR}/lxd.addr")
    (LXD_DIR=${LXD_REMOTE_DIR} deps/import-busybox --alias remoteimage --template start --public)

    token="$(LXD_DIR=${LXD_REMOTE_DIR} lxc config trust add --name foo -q)"
    lxc remote add l2 "${LXD_REMOTE_ADDR}" --token "${token}"

    # Relax all constraints except the disk limits, which won't be enough for the
    # image to be downloaded.
    lxc profile device set default root size=500KiB
    lxc project set p1 limits.disk 111MiB
    lxc project unset p1 limits.containers
    lxc project unset p1 limits.cpu
    lxc project unset p1 limits.memory
    lxc project unset p1 limits.processes

    # Can't download a remote image if that would exceed the current quota.
    ! lxc init l2:remoteimage c3 || false
  fi

  lxc storage volume delete "${pool}" v1
  lxc delete c1 c2
  lxc profile delete unrestricted

  lxc project switch default
  lxc project delete p1

  # Start with clean project.
  lxc project create p1
  lxc project switch p1

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  lxc project set p1 restricted.containers.lowlevel allow

  # Add a root device to the default profile of the project and import an image.
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  # Create a couple of containers in the project.
  lxc init --empty c1 -c limits.memory=1GiB -d "${SMALL_ROOT_DISK}"
  lxc init --empty c2 -c limits.memory=1GiB -d "${SMALL_ROOT_DISK}"

  lxc export c1
  lxc delete c1

  # Configure a valid project memory limit.
  lxc project set p1 limits.memory 1GiB

  # Can't import the backup as it would exceed the 1GiB project memory limit.
  ! lxc import c1.tar.gz || false

  rm c1.tar.gz
  lxc delete c2
  lxc project switch default
  lxc project delete p1

  if [ "${LXD_BACKEND}" = "dir" ] || [ "${LXD_BACKEND}" = "zfs" ]; then
    lxc remote remove l2
    kill_lxd "$LXD_REMOTE_DIR"
  fi
}

test_projects_restrictions() {
  run_projects_restrictions local # Unix socket

  ensure_has_localhost_remote "${LXD_ADDR}" # Unrestricted TLS client
  run_projects_restrictions localhost

  LXD_CONF1=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF1}" gen_cert_and_key "client"
  trust_token="$(lxc config trust add --name test-user1 --quiet --restricted)"
  LXD_CONF="${LXD_CONF1}" lxc remote add restricted "${trust_token}"
  LXD_CONF="${LXD_CONF1}" run_projects_restrictions restricted # Restricted TLS client

  LXD_CONF2=$(mktemp -d -p "${TEST_DIR}" XXX)
  LXD_CONF="${LXD_CONF2}" gen_cert_and_key "client"
  lxc auth group create test-group
  pending_identity_token="$(lxc auth identity create tls/test-user2 --quiet --group test-group)"
  LXD_CONF="${LXD_CONF2}" lxc remote add fine-grained "${pending_identity_token}"
  LXD_CONF="${LXD_CONF2}" run_projects_restrictions fine-grained # Fine-grained TLS client

  lxc config trust remove "$(cert_fingerprint "${LXD_CONF1}/client.crt")"
  lxc auth identity delete tls/test-user2
  rm -rf "${LXD_CONF1}" "${LXD_CONF2}"
}

# Set restrictions on projects.
run_projects_restrictions() {
  # Switch to the given remote. The remote name tells us what kind of access we have. We'll use the local: remote when
  # we need to perform privileged actions to set up test assertions.
  remote="${1}"
  lxc remote switch "${remote}"

  # Add a managed network.
  netManaged="lxd$$"
  lxc network create "local:${netManaged}" ipv4.address=none ipv6.address=none

  netUnmanaged="${netManaged}-unm"
  ip link add "${netUnmanaged}" type bridge

  # Create a project
  lxc project create local:p1 -c features.storage.volumes=false

  # Grant access to the project.
  if [ "${remote}" = "restricted" ]; then
    # For the restricted client we need to grant access to the project first.
    # shellcheck disable=SC2153
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"
    lxc config trust show "${fingerprint}" | sed -e 's/projects: \[\]/projects: ["p1"]/' | lxc config trust edit "local:${fingerprint}"
  elif [ "${remote}" = "fine-grained" ]; then
    # For the fine grained client we'll grant (almost) equivalent access to the restricted certificate, with the exception
    # that networks in the default project can never be modified by this user via the "punching through" that the feature flags allow
    # with restricted certs.
    lxc auth group permission add local:test-group project p1 operator
    lxc auth group permission add local:test-group project default can_view_networks
    lxc auth group permission add local:test-group server can_view_unmanaged_networks
  fi

  # Switch to the project. Also switch the local remote if not already set.
  lxc project switch p1
  if [ "${remote}" != "local" ]; then
    lxc remote switch local
    lxc project switch p1
    lxc remote switch "${remote}"
  fi

  # Check with restricted unset and restricted.devices.nic unset that managed & unmanaged networks are accessible.
  lxc network list | grep -F "${netManaged}"
  lxc network list | grep -F "${netUnmanaged}"
  lxc network show "${netManaged}"
  lxc network show "${netUnmanaged}"
  if [ "${remote}" = "local" ] || [ "${remote}" = "localhost" ] || [ "${remote}" = "fine-grained" ]; then
    lxc network list --all-projects | grep -F "${netManaged}"
    lxc network list --all-projects | grep -F "${netUnmanaged}"
  else
    ! lxc network list --all-projects || false
  fi

  # Check with restricted unset and restricted.devices.nic=block that managed & unmanaged networks are accessible.
  lxc project set local:p1 restricted.devices.nic=block
  lxc network list | grep -F "${netManaged}"
  lxc network list | grep -F "${netUnmanaged}"
  lxc network show "${netManaged}"
  lxc network show "${netUnmanaged}"
  if [ "${remote}" = "local" ] || [ "${remote}" = "localhost" ] || [ "${remote}" = "fine-grained" ]; then
    lxc network list --all-projects | grep -F "${netManaged}"
    lxc network list --all-projects | grep -F "${netUnmanaged}"
  else
    ! lxc network list --all-projects || false
  fi

  # Check with restricted=true and restricted.devices.nic=block that managed & unmanaged networks are inaccessible.
  lxc project set local:p1 restricted=true
  ! lxc network list | grep -F "${netManaged}"|| false
  ! lxc network show "${netManaged}" || false
  ! lxc network list | grep -F "${netUnmanaged}"|| false
  ! lxc network show "${netUnmanaged}" || false
  if [ "${remote}" = "local" ] || [ "${remote}" = "localhost" ] || [ "${remote}" = "fine-grained" ]; then
    # Can view when performing an --all-projects request because the network is actually defined in the default project,
    # and the default project is not restricted.
    lxc network list --all-projects | grep -F "${netManaged}"
    lxc network list --all-projects | grep -F "${netUnmanaged}"
  else
    ! lxc network list --all-projects || false
  fi

  # Check with restricted=true and restricted.devices.nic=managed that managed networks are accessible and that
  # unmanaged networks are inaccessible.
  lxc project set local:p1 restricted.devices.nic=managed
  lxc network list | grep -F "${netManaged}"
  lxc network show "${netManaged}"
  ! lxc network list | grep -F "${netUnmanaged}"|| false
  if [ "${remote}" = "local" ] || [ "${remote}" = "localhost" ] || [ "${remote}" = "fine-grained" ]; then
    # Can view when performing an --all-projects request because the network is actually defined in the default project,
    # and the default project is not restricted.
    lxc network list --all-projects | grep -F "${netManaged}"
    lxc network list --all-projects | grep -F "${netUnmanaged}"
  else
    ! lxc network list --all-projects || false
  fi

  # Check with restricted.devices.nic=allow and restricted.networks.access set to a network other than the existing
  # managed and unmanaged ones that they are inaccessible.
  lxc project set local:p1 restricted.devices.nic=allow
  lxc project set local:p1 restricted.networks.access=foo
  ! lxc network list | grep -F "${netManaged}"|| false
  ! lxc network show "${netManaged}" || false
  ! lxc network info "${netManaged}" || false
  if [ "${remote}" = "local" ] || [ "${remote}" = "localhost" ] || [ "${remote}" = "fine-grained" ]; then
    # Can view when performing an --all-projects request because the network is actually defined in the default project,
    # and the default project is not restricted.
    lxc network list --all-projects | grep -F "${netManaged}"
  else
    ! lxc network list --all-projects || false
  fi

  ! lxc network list | grep -F "${netUnmanaged}"|| false
  ! lxc network show "${netUnmanaged}" || false
  ! lxc network info "${netUnmanaged}" || false
  if [ "${remote}" = "local" ] || [ "${remote}" = "localhost" ] || [ "${remote}" = "fine-grained" ]; then
    # Can view when performing an --all-projects request because the network is actually defined in the default project,
    # and the default project is not restricted.
    lxc network list --all-projects | grep -F "${netUnmanaged}"
  else
    ! lxc network list --all-projects || false
  fi

  ! lxc network set "${netManaged}" user.foo=bah || false
  ! lxc network get "${netManaged}" ipv4.address || false
  ! lxc network info "${netManaged}" || false
  ! lxc network delete "${netManaged}" || false

  ! lxc profile device add default eth0 nic nictype=bridge parent=netManaged || false
  ! lxc profile device add default eth0 nic nictype=bridge parent=netUnmanaged || false

  ip link delete "${netUnmanaged}"

  # Disable restrictions to allow devices to be added to profile.
  lxc project unset local:p1 restricted.networks.access
  lxc project set local:p1 restricted.devices.nic=managed
  lxc project set local:p1 restricted=false

  # Add a root device to the default profile of the project and import an image.
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add local:default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage
  fingerprint="$(lxc image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a volume.
  lxc storage volume create "local:${pool}" "v-proj$$"

  # Enable all restrictions.
  lxc project set local:p1 restricted=true

  # It's not possible to create nested containers.
  ! lxc profile set default security.nesting=true || false
  ! lxc init testimage c1 -c security.nesting=true || false

  # It's not possible to use forbidden low-level options
  ! lxc profile set default "raw.idmap=both 0 0" || false
  ! lxc init testimage c1 -c "raw.idmap=both 0 0" || false
  ! lxc init testimage c1 -c volatile.uuid="foo" || false

  # It's not possible to create privileged containers.
  ! lxc profile set default security.privileged=true || false
  ! lxc init testimage c1 -c security.privileged=true || false

  # It's possible to create non-isolated containers.
  lxc init testimage c1 -c security.idmap.isolated=false -d "${SMALL_ROOT_DISK}"

  # It's not possible to change low-level options
  ! lxc config set c1 "raw.idmap=both 0 0" || false
  ! lxc config set c1 volatile.uuid="foo" || false

  # It's not possible to attach character devices.
  ! lxc profile device add default tty unix-char path=/dev/ttyS0 || false
  ! lxc config device add c1 tty unix-char path=/dev/ttyS0 || false

  # It's not possible to attach raw network devices.
  ! lxc profile device add default eth0 nic nictype=p2p || false

  # It's not possible to attach non-managed disk devices.
  ! lxc profile device add default testdir disk source="${TEST_DIR}" path=/mnt || false
  ! lxc config device add c1 testdir disk source="${TEST_DIR}" path=/mnt || false

  # It's possible to attach managed network devices.
  lxc profile device add default eth0 nic network="${netManaged}"

  # It's possible to attach disks backed by a pool.
  lxc config device add c1 data disk pool="${pool}" path=/mnt source="v-proj$$"

  # It's not possible to set restricted.containers.nic to 'block' because
  # there's an instance using the managed network.
  ! lxc project set local:p1 restricted.devices.nic=block || false

  # Relaxing restricted.containers.nic to 'allow' makes it possible to attach
  # raw network devices.
  lxc project set local:p1 restricted.devices.nic=allow
  lxc config device add c1 eth1 nic nictype=p2p

  # Relaxing restricted.containers.disk to 'allow' makes it possible to attach
  # non-managed disks.
  lxc project set local:p1 restricted.devices.disk=allow
  lxc config device add c1 testdir disk source="${TEST_DIR}" path=/foo

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  lxc project set local:p1 restricted.containers.lowlevel=allow
  lxc config set c1 "raw.idmap=both 0 0"

  lxc delete c1

  # Setting restricted.containers.disk to 'block' allows only the root disk
  # device.
  lxc project set local:p1 restricted.devices.disk=block
  ! lxc profile device add default data disk pool="${pool}" path=/mnt source="v-proj$$" || false

  restrictedDir="${TEST_DIR}/projects_restricted"
  mkdir "${restrictedDir}"
  tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)
  optDir=$(mktemp -d --tmpdir="${restrictedDir}")

  # Block unmanaged disk devices
  lxc project set local:p1 restricted.devices.disk=managed
  ! lxc profile device add default data disk path=/mnt source="${tmpDir}" || false

  # Allow unmanaged disk devices
  lxc project set local:p1 restricted.devices.disk=allow
  lxc profile device add default data disk path=/mnt source="${tmpDir}"
  lxc profile device remove default data

  # Path restrictions
  lxc project set local:p1 restricted.devices.disk.paths="${restrictedDir}"
  ! lxc profile device add default data disk path=/mnt source="${tmpDir}" || false
  lxc profile device add default data disk path=/mnt source="${optDir}"
  lxc profile device remove default data

  rm -r "${tmpDir}" "${optDir}" "${restrictedDir}"

  # Setting restricted.containers.nesting to 'allow' makes it possible to create
  # nested containers.
  lxc project set local:p1 restricted.containers.nesting=allow
  lxc init testimage c1 -c security.nesting=true -d "${SMALL_ROOT_DISK}"

  # It's not possible to set restricted.containers.nesting back to 'block',
  # because there's an instance with security.nesting=true.
  ! lxc project set local:p1 restricted.containers.nesting=block || false

  lxc delete c1

  # Setting restricted.containers.lowlevel to 'allow' makes it possible to set
  # low-level options.
  lxc project set local:p1 restricted.containers.lowlevel=allow
  lxc init testimage c1 -c "raw.idmap=both 0 0" -d "${SMALL_ROOT_DISK}"

  # It's not possible to set restricted.containers.lowlevel back to 'block',
  # because there's an instance with raw.idmap set.
  ! lxc project set local:p1 restricted.containers.lowlevel=block || false

  lxc delete c1

  # Setting restricted.containers.privilege to 'allow' makes it possible to create
  # privileged containers.
  lxc project set local:p1 restricted.containers.privilege=allow
  lxc init testimage c1 -c security.privileged=true -d "${SMALL_ROOT_DISK}"

  # It's not possible to set restricted.containers.privilege back to
  # 'unprivileged', because there's an instance with security.privileged=true.
  ! lxc project set local:p1 restricted.containers.privilege=unprivileged || false

  # Test expected syscall interception behavior.
  ! lxc config set local:c1 security.syscalls.intercept.mknod=true || false
  lxc config set local:c1 security.syscalls.intercept.mknod=false
  lxc project set local:p1 restricted.containers.interception=block
  ! lxc config set local:c1 security.syscalls.intercept.mknod=true || false
  lxc project set local:p1 restricted.containers.interception=allow
  lxc config set local:c1 security.syscalls.intercept.mknod=true
  lxc config set local:c1 security.syscalls.intercept.mount=true
  ! lxc config set local:c1 security.syscalls.intercept.mount.allow=ext4 || false

  lxc delete c1

  echo "==> Check that restricted.* options are not checked during project update if restricted=false."

  echo "==> Set project restricted=false."
  lxc project set local:p1 restricted=false
  echo "==> Set project restricted.virtual-machines.lowlevel=block."
  lxc project set local:p1 restricted.virtual-machines.lowlevel=block

  echo "==> Create an instance and mount a disk device to it with io.threads=4."
  lxc init --vm --empty v1 -d "${SMALL_ROOT_DISK}"
  # Device is allowed to use `io.threads` despite `restricted.virtual-machines.lowlevel=block` because `restricted!=true`.
  lxc config device add v1 foo disk source=/mnt path=/mnt io.threads=4

  echo "==> Check that project update succeeds."
  lxc project set local:p1 restricted.virtual-machines.lowlevel=allow

  echo "==> Clean up the instance."
  lxc delete v1

  lxc image delete testimage

  lxc profile device remove local:default root
  lxc project switch default || true
  lxc remote switch local
  lxc project switch default
  lxc project delete p1

  lxc network delete "${netManaged}"
  lxc storage volume delete "${pool}" "v-proj$$"
  rm -rf "${restrictedDir}"
}

# Test project state api
test_projects_usage() {
  # Set configuration on the default project
  lxc project create test-usage \
    -c limits.cpu=5 \
    -c limits.memory=1GiB \
    -c limits.disk=10GiB \
    -c limits.networks=3 \
    -c limits.processes=40

  # Create a profile defining resource allocations
  lxc profile show default --project default | lxc profile edit default --project test-usage
  lxc profile set default --project test-usage \
    limits.cpu=1 \
    limits.memory=512MiB \
    limits.processes=20
  lxc profile device set default root size=48MiB --project test-usage

  # Spin up a container
  deps/import-busybox --project test-usage --alias testimage
  lxc init testimage c1 --project test-usage
  lxc project info test-usage

  lxc project info test-usage --format csv | grep -xF "CONTAINERS,UNLIMITED,1"
  lxc project info test-usage --format csv | grep -xF "CPU,5,1"
  lxc project info test-usage --format csv | grep -xF "DISK,10.00GiB,48.00MiB"
  lxc project info test-usage --format csv | grep -xF "INSTANCES,UNLIMITED,1"
  lxc project info test-usage --format csv | grep -xF "MEMORY,1.00GiB,512.00MiB"
  lxc project info test-usage --format csv | grep -xF "NETWORKS,3,0"
  lxc project info test-usage --format csv | grep -xF "PROCESSES,40,20"
  lxc project info test-usage --format csv | grep -xF "VIRTUAL-MACHINES,UNLIMITED,0"

  lxc delete c1 --project test-usage
  lxc image delete testimage --project test-usage
  lxc project delete test-usage
}

test_projects_yaml() {
  lxc project create test-project-yaml <<EOF
config:
  limits.cpu: 2
  limits.memory: 2048MiB
description: Test project using YAML
EOF

  lxc profile show default --project default | lxc profile edit default --project test-project-yaml
  lxc profile set default --project test-project-yaml \
    limits.cpu=1 \
    limits.memory=512MiB

  lxc profile device set default root size=300MiB --project test-project-yaml
  deps/import-busybox --project test-project-yaml --alias testimage

  lxc init testimage c1 --project test-project-yaml -d "${SMALL_ROOT_DISK}"
  lxc init testimage c2 --project test-project-yaml -d "${SMALL_ROOT_DISK}"
  ! lxc init testimage c3 --project test-project-yaml || false # Should fail due to the project limits.cpu=2 (here we would have 3 containers with 1 CPU each)

  lxc delete -f c1 --project test-project-yaml
  lxc delete -f c2 --project test-project-yaml

  lxc image delete testimage --project test-project-yaml
  lxc project delete test-project-yaml
}

# Test project operations with an uninitialized LXD.
test_projects_before_init() {
  LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_INIT_DIR}" false

  # Check if projects can be created and modified without any pre-existing storage pools.
  LXD_DIR=${LXD_INIT_DIR} lxc project create foo --config user.foo=bar
  [ "$(LXD_DIR=${LXD_INIT_DIR} lxc project get foo user.foo)" = "bar" ]

  shutdown_lxd "${LXD_INIT_DIR}"
}

test_projects_images_volume() {
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc storage volume create "${pool}" vol

  # Can't be set on non-existing projects
  ! lxc config set storage.project.foo.images_volume="${pool}/vol" || false

  lxc project create foo

  if [ "${LXD_BACKEND}" = "ceph" ]; then
    # This won't work on ceph because it's not a multi-node storage
    ! lxc config set storage.project.foo.images_volume="${pool}/vol" || false
    # Clean up
    lxc project delete foo
    lxc storage volume delete "${pool}" vol
    return
  fi

  # Check that projects without images can't have dedicated images storage
  lxc project set foo features.images=false
  ! lxc config set storage.project.foo.images_volume="${pool}/vol" || false
  lxc project set foo features.images=true

  lxc config set storage.project.foo.images_volume="${pool}/vol"

  # It should be possible to change the setting on empty projects
  lxc config unset storage.project.foo.images_volume
  lxc config set storage.project.foo.images_volume="${pool}/vol"

  # Import an image into the project and grab its fingerprint
  lxc project switch foo
  ensure_import_testimage
  fingerprint="$(lxc image list -f csv -c F testimage)"
  lxc project switch default

  # The image should not exist in the default storage, only in the project images volume
  [ ! -e "${LXD_DIR}/images/${fingerprint}" ]
  [ -z "$(ls -A "${LXD_DIR}/images")" ]
  [ -f "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/images/${fingerprint}" ]

  # It should not be possible to change the setting on non-empty projects
  ! lxc config unset storage.project.foo.images_volume || false

  # It should be possible to share the volume among multiple projects
  lxc project create foo2
  lxc config set storage.project.foo2.images_volume="${pool}/vol"
  [ "$(lxc config get storage.project.foo2.images_volume)" = "${pool}/vol" ]
  lxc project switch foo2
  ensure_import_testimage
  lxc image delete testimage
  lxc project switch default
  lxc project delete foo2

  # Removal of the project should clear the setting too
  [ "$(lxc config get storage.project.foo2.images_volume)" = "" ]

  # Import the image in the default project and storage, and ensure it's gone after removal
  ensure_import_testimage
  [ -e "${LXD_DIR}/images/${fingerprint}" ]
  lxc image delete testimage
  [ ! -e "${LXD_DIR}/images/${fingerprint}" ]
  [ -z "$(ls -A "${LXD_DIR}/images")" ]

  # It should not be possible to move daemon storage to the same volume
  ! lxc config set storage.images_volume="${pool}/vol" || false

  # Create a container in the project
  lxc init --project foo --storage "${pool}" testimage c1 -d "${SMALL_ROOT_DISK}"

  # Delete the container
  lxc delete --project foo c1

  # Delete the image
  lxc image delete --project foo testimage
  [ -z "$(ls -A "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/images")" ]

  # Clean up
  lxc project delete foo
  lxc storage volume delete "${pool}" vol
}

test_projects_backups_volume() {
  pool="lxdtest-$(basename "${LXD_DIR}")"
  lxc storage volume create "${pool}" vol

  # Can't be set on non-existing projects
  ! lxc config set storage.project.foo.backups_volume="${pool}/vol" || false

  lxc project create foo

  if [ "${LXD_BACKEND}" = "ceph" ]; then
    # This won't work on ceph because it's not a multi-node storage
    ! lxc config set storage.project.foo.backups_volume="${pool}/vol" || false
    # Clean up
    lxc project delete foo
    lxc storage volume delete "${pool}" vol
    return
  fi

  # Create test project sharing the storage for images and backups
  lxc config set storage.project.foo.images_volume="${pool}/vol"
  lxc config set storage.project.foo.backups_volume="${pool}/vol"

  # It should be possible to change the setting on empty projects
  lxc config unset storage.project.foo.backups_volume
  lxc config set storage.project.foo.backups_volume="${pool}/vol"

  # It should not be possible to move daemon storage to the same volume
  ! lxc config set storage.backups_volume="${pool}/vol" || false

  # Import an image into the project
  lxc project switch foo

  # Create a container in the project
  lxc init --storage "${pool}" --empty c1 -d "${SMALL_ROOT_DISK}"

  # Take the backup
  lxc query -X POST --wait -d '{\"name\":\"bak\"}' "/1.0/instances/c1/backups?project=foo"

  # Make sure the backup does not exist in the default storage
  [ -d "${LXD_DIR}/backups/instances" ]
  [ -z "$(ls -A "${LXD_DIR}/backups/instances")" ]

  # Make sure the backup does exist in the dedicated pool
  [ -d "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/backups/instances" ]
  [ -d "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/backups/instances/foo_c1" ]
  [ -f "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/backups/instances/foo_c1/bak" ]

  # Delete the container
  lxc delete c1

  # Ensure the backup is gone
  [ -d "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/backups/instances" ]
  [ -z "$(ls -A "${LXD_DIR}/storage-pools/${pool}/custom/default_vol/backups/instances")" ]

  # Clean up
  lxc project switch default
  lxc project delete foo
  lxc storage volume delete "${pool}" vol
}
