test_template() {
  local lxd_backend
  lxd_backend=$(storage_backend "$LXD_DIR")

  if [ "${lxd_backend}" = "dir" ] && uname -r | grep -- -kvm$; then
    export TEST_UNMET_REQUIREMENT="The -kvm kernel flavor does not work for this test on ${lxd_backend}"
    return 0
  fi

  echo "Import a template which only triggers on create"
  deps/import-busybox --alias template-test --template create
  lxc init template-test template

  echo "Confirm that template application is delayed to first start"
  ! lxc file pull template/template - || false

  echo "Validate that the template is applied"
  lxc start template
  lxc file pull template/template - | grep -xF "name: template"

  if [ "$lxd_backend" = "lvm" ] || [ "$lxd_backend" = "ceph" ]; then
    lxc stop template --force
  fi

  echo "Confirm it's not applied on copies"
  lxc copy template template1
  lxc file pull template1/template - | grep -xF "name: template"

  # Cleanup
  lxc image delete template-test
  lxc delete template template1 --force


  echo "Import a template which only triggers on copy"
  deps/import-busybox --alias template-test --template copy
  lxc launch template-test template

  echo "Confirm that the template doesn't trigger on create"
  ! lxc file pull template/template - || false
  if [ "$lxd_backend" = "lvm" ] || [ "$lxd_backend" = "ceph" ]; then
    lxc stop template --force
  fi

  echo "Copy the container"
  lxc copy template template1

  echo "Confirm that template application is delayed to first start"
  ! lxc file pull template1/template - || false

  echo "Validate that the template is applied"
  lxc start template1
  lxc file pull template1/template - | grep -xF "name: template1"

  # Cleanup
  lxc image delete template-test
  lxc delete template template1 --force


  echo "Import a template which only triggers on start"
  deps/import-busybox --alias template-test --template start
  lxc launch template-test template

  echo "Validate that the template is applied"
  lxc file pull template/template - | grep -xF "name: template"
  lxc file pull template/template - | grep -xF "user.foo: _unset_"

  echo "Confirm it's re-run at every start"
  lxc config set template user.foo bar
  lxc restart template --force
  lxc file pull template/template - | grep -xF "user.foo: bar"

  # Cleanup
  lxc image delete template-test
  lxc delete template --force


  echo "Import a template which triggers on both create and copy"
  deps/import-busybox --alias template-test --template create,copy
  lxc init template-test template

  echo "Confirm that template application is delayed to first start"
  ! lxc file pull template/template - || false

  echo "Validate that the template is applied"
  lxc start template
  lxc file pull template/template - | grep -xF "name: template"

  echo "Confirm it's also applied on copies"
  lxc copy template template1
  lxc start template1
  lxc file pull template1/template - | grep -xF "name: template1"
  lxc file pull template1/template - | grep -xF "user.foo: _unset_"

  echo "But doesn't change on restart"
  lxc config set template1 user.foo bar
  lxc restart template1 --force
  lxc file pull template1/template - | grep -xF "user.foo: _unset_"

  # Cleanup
  lxc image delete template-test
  lxc delete template template1 --force
}
