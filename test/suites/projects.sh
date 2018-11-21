# Use the default project.
test_projects_default() {
  # The default project is used by the default profile
  lxc project show default | grep -q "/1.0/profiles/default?project=default"

  # Containers and images are assigned to the default project
  ensure_import_testimage
  lxc init testimage c1
  lxc project show default | grep -q "/1.0/profiles/default?project=default"
  lxc project show default | grep -q "/1.0/images/"
  lxc delete c1
}

# CRUD operations on project.
test_projects_crud() {
  # Create a project
  lxc project create foo

  # All features are enabled by default
  lxc project show foo | grep -q 'features.images: "true"'
  lxc project get foo "features.profiles" | grep -q 'true'

  # Trying to create a project with the same name fails
  ! lxc project create foo

  # Rename the project
  lxc project rename foo bar

  # Edit the project
  lxc project show bar| sed 's/^description:.*/description: "Bar project"/' | lxc project edit bar
  lxc project show bar | grep -q "description: Bar project"

  # Create a second project
  lxc project create foo

  # Trying to rename a project using an existing name fails
  ! lxc project rename bar foo

  lxc project switch foo

  # Turning off the profiles feature makes the project see the default profile
  # from the default project.
  lxc project set foo features.profiles false
  lxc profile show default | grep -E -q '^description: Default LXD profile$'

  # Turning on the profiles feature creates a project-specific default
  # profile.
  lxc project set foo features.profiles true
  lxc profile show default | grep -E -q '^description: Default LXD profile for project foo$'

  lxc project switch default

  # Delete the projects
  lxc project delete foo
  lxc project delete bar

  # We're back to the default project
  lxc project list | grep -q "default (current)"
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
  lxc init testimage c1

  # The container is listed when using this project
  lxc list | grep -q c1
  lxc info c1 | grep -q "Name: c1"

  # The container's volume is listed too.
  lxc storage volume list "${pool}" | grep -q c1

  # For backends with optimized storage, we can see the image volume inside the
  # project.
  driver="$(storage_backend "$LXD_DIR")"
  if [ "${driver}" != "dir" ]; then
      lxc storage volume list "${pool}" | grep -q "${fingerprint}"
  fi

  # Start the container
  lxc start c1
  lxc list | grep c1 | grep -q RUNNING
  echo "abc" | lxc exec c1 cat | grep -q abc

  # The container can't be managed when using the default project
  lxc project switch default
  ! lxc list | grep -q c1
  ! lxc info c1
  ! lxc delete c1
  ! lxc storage volume list "${pool}" | grep -q c1

  # Trying to delete a project which is in use fails
  ! lxc project delete foo

  # Trying to change features of a project which is in use fails
  ! lxc project show foo| sed 's/features.profiles:.*/features.profiles: "false"/' | lxc project edit foo
  ! lxc project set foo "features.profiles" "false"
  lxc project show foo | grep -q 'features.profiles: "true"'

  # Create a container with the same name in the default project
  ensure_import_testimage
  lxc init testimage c1
  lxc start c1
  lxc list | grep c1 | grep -q RUNNING
  lxc stop --force c1

  # Delete the container
  lxc project switch foo

  lxc stop --force c1
  lxc delete c1
  lxc image delete testimage

  # Delete the project
  lxc project delete foo

  # The container in the default project can still be used
  lxc start c1
  lxc list | grep c1 | grep -q RUNNING
  lxc stop --force c1
  lxc delete c1
}

# Copy/move between projects
test_projects_copy() {
  ensure_import_testimage

  # Create a couple of projects
  lxc project create foo -c features.profiles=false -c features.images=false
  lxc project create bar -c features.profiles=false -c features.images=false

  # Create a container in the project
  lxc --project foo init testimage c1
  lxc --project foo copy c1 c1 --target-project bar
  lxc --project bar start c1
  lxc --project bar delete c1 -f

  lxc --project foo snapshot c1
  lxc --project foo snapshot c1
  lxc --project foo snapshot c1

  lxc --project foo copy c1 c1 --target-project bar
  lxc --project foo start c1
  lxc --project bar start c1

  lxc --project foo delete c1 -f
  lxc --project bar stop c1 -f
  lxc --project bar move c1 c1 --target-project foo
  lxc --project foo start c1
  lxc --project foo delete c1 -f

  # Clean things up
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
  lxc init testimage c1

  # Create, rename and delete a snapshot
  lxc snapshot c1
  lxc info c1 | grep -q snap0
  lxc config show c1/snap0 | grep -q Busybox
  lxc rename c1/snap0 c1/foo
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
  lxc init testimage c1
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

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create a container in the project
  lxc init testimage c1

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
  lxc image delete testimage
  lxc project delete foo
}

# Use private profiles in a project.
test_projects_profiles() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # List profiles
  lxc profile list | grep -q 'default'
  lxc profile show default | grep -q 'description: Default LXD profile for project foo'

  # Create a profile in this project
  lxc profile create p1
  lxc profile list | grep -q 'p1'

  # Set a config key on this profile
  lxc profile set p1 user.x y
  lxc profile get p1 user.x | grep -q 'y'

  # The profile is not visible in the default project
  lxc project switch default
  ! lxc profile list | grep -q 'p1'

  # A profile with the same name can be created in the default project
  lxc profile create p1

  # The same key can have a different value
  lxc profile set p1 user.x z
  lxc profile get p1 user.x | grep -q 'z'

  # Switch back to the project
  lxc project switch foo

  # The profile has still the original config
  lxc profile get p1 user.x | grep -q 'y'

  # Delete the profile from the project
  lxc profile delete p1

  # Delete the project
  lxc project delete foo

  # Delete the profile from the default project
  lxc profile delete p1
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
  lxc init "${fingerprint}" c1

  # Switch back the default project
  lxc project switch default

  # Create a container in the default project as well.
  ensure_import_testimage
  lxc init testimage c1

  # If we look at the global profile we see that it's being used by both the
  # container in the above project and the one we just created.
  lxc profile show default | grep -E -q '^- /1.0/containers/c1$'
  lxc profile show default | grep -E -q '^- /1.0/containers/c1\?project=foo$'

  lxc delete c1

  lxc project switch foo

  # Delete the project
  lxc delete c1
  lxc image delete "${fingerprint}"
  lxc project delete foo
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
  ! lxc image list | grep -q "${fingerprint}"

  # Switch back to the project and clean it up.
  lxc project switch foo
  lxc image delete "${fingerprint}"

  # Now Import an image into the project assigning it an alias
  deps/import-busybox --project foo --alias foo-image

  # The image alias shows up in the project
  lxc image list | grep -q foo-image

  # However the image alias is not visible in the default project.
  lxc project switch default
  ! lxc image list | grep -q foo-project

  # Let's import the same image in the default project
  ensure_import_testimage

  # Switch back to the project.
  lxc project switch foo

  # The image alias from the default project is not visiable here
  ! lxc image list | grep -q testimage

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
  lxc image list | grep -q testimage
}

# Use global images in a project.
test_projects_images_default() {
  # Make sure that there's an image in the default project
  ensure_import_testimage

  # Create a new project, without the features.images config.
  lxc project create foo
  lxc project switch foo
  lxc project set foo "features.images" "false"

  # The project can see images from the defaut project
  lxc image list | grep -q testimage

  # The project can delete images in the default project
  lxc image delete testimage

  # Images imported into the project show up in the default project
  deps/import-busybox --project foo --alias foo-image
  lxc image list | grep -q foo-image
  lxc project switch default
  lxc image list | grep -q foo-image

  lxc image delete foo-image

  lxc project delete foo
}

# Interaction between projects and storage pools.
test_projects_storage() {
  pool="lxdtest-$(basename "${LXD_DIR}")"

  lxc storage volume create "${pool}" vol

  lxc project create foo
  lxc project switch foo

  lxc storage volume list "${pool}" | grep custom | grep -q vol

  lxc storage volume delete "${pool}" vol

  lxc project switch default

  ! lxc storage volume list "${pool}" | grep -q custom

  lxc project delete foo
}

# Interaction between projects and networks.
test_projects_network() {
  # Standard bridge with random subnet and a bunch of options
  network="lxdt$$"
  lxc network create "${network}"

  lxc project create foo
  lxc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"

  # Create a container in the project
  lxc init -n "${network}" testimage c1

  lxc network show "${network}" |grep -q "/1.0/containers/c1?project=foo"

  # Delete the container
  lxc delete c1

  # Delete the project
  lxc image delete testimage
  lxc project delete foo

  lxc network delete "${network}"
}
