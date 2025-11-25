_image_nil_profile_list() {
  # Launch container with default profile list and check its profiles
  ensure_import_testimage
  lxc init testimage c1
  [ "$(lxc list -f json c1 | jq -r '.[0].profiles | join(" ")')" = "default" ]

  # Cleanup
  lxc delete c1
  lxc image delete testimage
}

_image_empty_profile_list() {
  # Set the profiles to be an empty list
  ensure_import_testimage
  lxc image show testimage | sed "s/profiles.*/profiles: []/; s/- default//" | lxc image edit testimage

  # Check that the profile list is correct
  lxc image show testimage | grep -xF 'profiles: []'
  ! lxc image show testimage | grep -xF -- '- default' || false

  # Launch the container and check its profiles
  storage="$(lxc storage list -f csv | tail -n1 | cut -d, -f1)"
  lxc init testimage c1 -s "$storage"
  [ "$(lxc list -f json c1 | jq -r '.[0].profiles | join(" ")')" = "" ]

  # Cleanup
  lxc delete c1
  lxc image delete testimage
}

_image_alternate_profile_list() {
  # Add three new profiles to the profile list
  ensure_import_testimage
  lxc profile create p1
  lxc profile create p2
  lxc profile create p3
  lxc image show testimage | sed "s/profiles.*/profiles: ['p1','p2','p3']/; s/- default//" | lxc image edit testimage

  # Check that the profile list is correct
  lxc image show testimage | grep -xF -- '- p1'
  lxc image show testimage | grep -xF -- '- p2'
  lxc image show testimage | grep -xF -- '- p3'
  ! lxc image show testimage | grep -xF -- '- default' || false

  # Launch the container and check its profiles
  storage="$(lxc storage list -f csv | tail -n1 | cut -d, -f1)"
  lxc profile device add p1 root disk path=/ pool="$storage"
  lxc init testimage c1
  [ "$(lxc list -f json c1 | jq -r '.[0].profiles | join(" ")')" = "p1 p2 p3" ]

  # Cleanup
  lxc delete c1
  lxc profile delete p1
  lxc profile delete p2
  lxc profile delete p3
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
  storage="$(lxc storage list -f csv | tail -n1 | cut -d, -f1)"
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
  storage="$(lxc storage list -f csv | tail -n1 | cut -d, -f1)"
  lxc profile device add default root disk path=/ pool="$storage"

  _image_nil_profile_list
  _image_empty_profile_list
  _image_alternate_profile_list

  lxc project switch default
  lxc project delete project1
}
