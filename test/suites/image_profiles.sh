_image_nil_profile_list() {
  # Launch container with default profile list and check its profiles
  ensure_import_testimage
  lxc init testimage c1
  lxc list -f json c1 | jq --exit-status '.[0].profiles == ["default"]'

  # Cleanup
  lxc delete c1
}

_image_empty_profile_list() {
  # Set the profiles to be an empty list
  ensure_import_testimage
  lxc image show testimage | sed "s/profiles.*/profiles: []/; s/- default//" | lxc image edit testimage

  # Check that the profile list is correct
  local testimageShow
  testimageShow="$(lxc image show testimage)"
  grep -xF 'profiles: []' <<<"${testimageShow}"
  ! grep -xF -- '- default' <<<"${testimageShow}" || false

  # Launch the container and check its profiles
  storage="lxdtest-$(basename "${LXD_DIR}")"
  lxc init testimage c1 -s "$storage"
  lxc list -f json c1 | jq --exit-status '.[0].profiles == []'

  # Cleanup
  lxc delete c1
  lxc image delete testimage
}

_image_alternate_profile_list() {
  # Add three new profiles to the profile list
  ensure_import_testimage
  lxc profile create p1
  lxc profile create p2
  lxc image show testimage | sed "s/profiles.*/profiles: ['p1','p2']/; s/- default//" | lxc image edit testimage

  # Check that the profile list is correct
  local testimageShow
  testimageShow="$(lxc image show testimage)"
  grep -xF -- '- p1' <<<"${testimageShow}"
  grep -xF -- '- p2' <<<"${testimageShow}"
  ! grep -xF -- '- default' <<<"${testimageShow}" || false

  # Launch the container and check its profiles
  storage="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add p1 root disk path=/ pool="$storage"
  lxc init testimage c1
  lxc list -f json c1 | jq --exit-status '.[0].profiles == ["p1","p2"]'

  # Cleanup
  lxc delete c1
  lxc profile delete p1
  lxc profile delete p2
  lxc image delete testimage
}

test_profiles_project_default() {
  lxc project switch default
  _image_nil_profile_list
  _image_empty_profile_list
  _image_alternate_profile_list
}

test_profiles_project_images_profiles() {
  lxc project create project1
  lxc project switch project1
  storage="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add default root disk path=/ pool="$storage"

  _image_nil_profile_list
  _image_empty_profile_list
  _image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}

# Run the tests with a project that only has the features.images enabled
test_profiles_project_images() {
  lxc project create project1 -c features.profiles=false
  lxc project switch project1

  _image_nil_profile_list
  _image_empty_profile_list
  _image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}

test_profiles_project_profiles() {
  lxc project create project1 -c features.images=false
  lxc project switch project1
  storage="lxdtest-$(basename "${LXD_DIR}")"
  lxc profile device add default root disk path=/ pool="$storage"

  _image_nil_profile_list
  _image_empty_profile_list
  _image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}
