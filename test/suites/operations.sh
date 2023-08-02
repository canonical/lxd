test_operations_get_all() {
  assert_get_all_operations() {
    (
      set -e
      echo "==> get all-projects operations normally"
      result="$(lxc query "/1.0/operations?all-projects=true" | jq '.success[0]' | wc -l)"
      test "${result}" -eq 1 || false

      echo "==> get all-projects operations recursively"
      result="$(lxc query "/1.0/operations?recursion=1&all-projects=true" | jq '.success[0]' | grep -c 'status_code')"
      test "${result}" -eq 1 || false
    )
  }

  ensure_import_testimage

  # create container
  name=x1
  lxc launch testimage ${name}
  lxc list ${name} | grep RUNNING

  # get_all_operations
  lxc query -X POST -d '{\"command\":[\"touch\",\"/root/foo1\"],\"record-output\":false}' /1.0/instances/${name}/exec
  sleep 1
  assert_get_all_operations
  
  lxc stop "${name}" --force
  lxc delete "${name}"
}

test_operations_get_by_project() {
  assert_get_project_operations() {
    # $1: query param for project name
    (
      set -e
      echo "==> get $1 operations normally"
      result="$(lxc query "/1.0/operations?project=$1" | jq '.success[0]' | wc -l)"
      test "${result}" -eq 1 || false

      echo "==> get $1 operations recursively"
      result="$(lxc query "/1.0/operations?recursion=1&project=$1" | jq '.success[0]' | grep -c 'status_code')"
      test "${result}" -eq 1 || false
    )
  }

  ensure_import_testimage

  project="default"

  # create container
  name=x1
  lxc launch testimage ${name} --project ${project}
  lxc list ${name} --project ${project} | grep RUNNING

  # get opetaions with project name
  lxc query -X POST -d '{\"command\":[\"touch\",\"/root/foo1\"],\"record-output\":false}' /1.0/instances/${name}/exec?project=${project}
  sleep 1
  assert_get_project_operations ${project}

  lxc stop "${name}" --force --project ${project}
  lxc delete "${name}" --project ${project}
}
