test_image_nil_profile_list() {
  # Launch container with default profile list and check its profiles
  ensure_import_testimage
  lxc launch testimage c1
  lxc info c1 | grep -q "Profiles: default" || false

  # Cleanup
  lxc stop c1
  lxc delete c1
  lxc image delete testimage
}

test_image_empty_profile_list() {
  # Set the profiles to be an empty list
  ensure_import_testimage
  lxc image show testimage | sed "s/profiles.*/profiles: []/; s/- default//" | lxc image edit testimage

  # Check that the profile list is correct
  lxc image show testimage | grep -q 'profiles: \[\]' || false
  ! lxc image show testimage | grep -q -- '- default' || false

  # Launch the container and check its profiles
  storage=$(lxc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  lxc launch testimage c1 -s "$storage"
  lxc info c1 | grep -q "Profiles: $" || false

  # Cleanup
  lxc stop c1
  lxc delete c1
  lxc image delete testimage
}

test_image_alternate_profile_list() {
  # Add three new profiles to the profile list
  ensure_import_testimage
  lxc profile create p1
  lxc profile create p2
  lxc profile create p3
  lxc image show testimage | sed "s/profiles.*/profiles: ['p1','p2','p3']/; s/- default//" | lxc image edit testimage

  # Check that the profile list is correct
  lxc image show testimage | grep -q -- '- p1' || false
  lxc image show testimage | grep -q -- '- p2' || false
  lxc image show testimage | grep -q -- '- p3' || false
  ! lxc image show testimage | grep -q -- '- default' || false

  # Launch the container and check its profiles
  storage=$(lxc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  lxc profile device add p1 root disk path=/ pool="$storage"
  lxc launch testimage c1
  lxc info c1 | grep -q "Profiles: p1, p2, p3" || false

  # Cleanup
  lxc stop c1
  lxc delete c1
  lxc profile delete p1
  lxc profile delete p2
  lxc profile delete p3
  lxc image delete testimage
}

test_profiles_project_default() {
  lxc project switch default
  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list
}

test_profiles_project_images_profiles() {
  lxc project create project1
  lxc project switch project1
  storage=$(lxc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  lxc profile device add default root disk path=/ pool="$storage"

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}

# Run the tests with a project that only has the features.images enabled
test_profiles_project_images() {
  lxc project create project1 -c features.profiles=false
  lxc project switch project1

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}

test_profiles_project_profiles() {
  lxc project create project1 -c features.images=false
  lxc project switch project1
  storage=$(lxc storage list | grep "^| " | tail -n 1 | cut -d' ' -f2)
  lxc profile device add default root disk path=/ pool="$storage"

  test_image_nil_profile_list
  test_image_empty_profile_list
  test_image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}
