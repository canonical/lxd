test_get_operations() {
  ensure_import_testimage

  ! lxc operation list --project nonexistent || false
  lxc operation list --project default

  proj1="op-proj1"
  proj2="op-proj2"

  (
    set -e

    lxc project create "${proj1}" -c features.profiles=false -c features.images=false
    lxc project create "${proj2}" -c features.profiles=false -c features.images=false

    lxc launch testimage c1 --project "${proj1}"
    lxc launch testimage c2 --project "${proj2}"

    # For each project, generate a single operation.
    lxc exec -T --project="${proj1}" c1 true
    lxc exec -T --project="${proj2}" c2 true

    # Get the operations output json with recursion=1
    proj1_full_ops_json=$(lxc query "/1.0/operations?project=${proj1}&recursion=1")
    proj2_full_ops_json=$(lxc query "/1.0/operations?project=${proj2}&recursion=1")
    all_full_ops_json=$(lxc query "/1.0/operations?all-projects=true&recursion=1")

    # Verify that both individual project operations and the collective set of operations are queried correctly.
    proj1_count=$(jq --exit-status '[.success[] | select(.description == "Executing command")] | length' <<< "${proj1_full_ops_json}")
    test "${proj1_count}" -eq 1
    proj2_count=$(jq --exit-status '[.success[] | select(.description == "Executing command")] | length' <<< "${proj2_full_ops_json}")
    test "${proj2_count}" -eq 1
    all_count=$(jq --exit-status '[.success[] | select(.description == "Executing command")] | length' <<< "${all_full_ops_json}")
    test "${all_count}" -eq 2

    proj1_op_id=$(jq --exit-status -r '.success[] | select(.description == "Executing command") | .id' <<< "${proj1_full_ops_json}")
    proj2_op_id=$(jq --exit-status -r '.success[] | select(.description == "Executing command") | .id' <<< "${proj2_full_ops_json}")

    proj1_ops_json=$(lxc query "/1.0/operations?project=${proj1}")
    proj2_ops_json=$(lxc query "/1.0/operations?project=${proj2}")
    all_ops_json=$(lxc query "/1.0/operations?all-projects=true")

    # Assert that the operations with these IDs exist across all projects.
    jq --exit-status --arg id "${proj1_op_id}" '.success | contains(["/1.0/operations/\($id)"])' <<< "${all_ops_json}"
    jq --exit-status --arg id "${proj2_op_id}" '.success | contains(["/1.0/operations/\($id)"])' <<< "${all_ops_json}"
    # Assert that the operations with these IDs exist within their respective projects.
    jq --exit-status --arg id "${proj1_op_id}" '.success | contains(["/1.0/operations/\($id)"])' <<< "${proj1_ops_json}"
    jq --exit-status --arg id "${proj2_op_id}" '.success | contains(["/1.0/operations/\($id)"])' <<< "${proj2_ops_json}"

    lxc delete c1 --force --project "${proj1}"
    lxc delete c2 --force --project "${proj2}"
  )
}

test_bulk_operation_children() {
  ensure_import_testimage

  (
    set -e

    # Create two containers to generate a bulk operation with child operations.
    lxc launch testimage c1
    lxc launch testimage c2

    # Trigger a bulk state change, which creates a parent operation with one child per instance.
    bulk_op_id=$(lxc query -X PUT /1.0/instances -d '{"state": {"action": "stop"}}' | jq -r '.id')

    # Wait for the bulk operation to complete.
    lxc query "/1.0/operations/${bulk_op_id}/wait?timeout=60" > /dev/null

    # Assert the CHILDREN column in lxc operation list shows the correct child count.
    child_count=$(lxc operation list --format json | jq --arg id "${bulk_op_id}" '.[] | select(.id == $id) | .child_count')
    [ "${child_count}" -eq 2 ]

    # Assert child_count is accurate on a non-recursive single GET (no recursion=1 required).
    api_child_count=$(lxc query "/1.0/operations/${bulk_op_id}" | jq '.child_count')
    [ "${api_child_count}" -eq 2 ]

    # Assert lxc operation list-children shows the expected number of child operations.
    list_children_count=$(lxc operation list-children "${bulk_op_id}" --format json | jq 'length')
    [ "${list_children_count}" -eq 2 ]

    lxc delete c1 --force
    lxc delete c2 --force
  )
}

test_operations_conflict_reference() {
  conflictRef="test-conflict-ref"

  # operation-wait requires instance for entity_type
  lxc init --empty c1

  # Create two operations with the same conflict_reference. The second creation should fail.
  # op_type 75 is "Wait" operation.
  lxc query -X POST '/internal/testing/operation-wait' -d '{"duration": "5s", "op_class": 1, "op_type": 75, "entity_url": "/1.0/instances/c1", "conflict_reference": "'"${conflictRef}"'"}'
  ! lxc query -X POST '/internal/testing/operation-wait' -d '{"duration": "5s", "op_class": 1, "op_type": 75, "entity_url": "/1.0/instances/c1", "conflict_reference": "'"${conflictRef}"'"}' || false

  lxc delete c1 --force
}
