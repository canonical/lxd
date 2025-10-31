test_warnings() {
    # Delete previous warnings
    lxc warning delete --all

    # Ensure that listing warnings in a project that doesn't exist fails
    ! lxc warning list --project nonexistent || false

    # Create a global warning (no node and no project)
    lxc query --wait -X POST -d '{"type_code": 0, "message": "global warning"}' /internal/testing/warnings

    # More valid queries
    lxc query --wait -X POST -d '{"type_code": 0, "message": "global warning", "project": "default"}' /internal/testing/warnings

    # Update the last warning. This will not create a new warning.
    lxc query --wait -X POST -d '{"type_code": 0, "message": "global warning 2", "project": "default"}' /internal/testing/warnings

    # There should be two warnings now.
    lxc query --wait /1.0/warnings | jq --exit-status 'length == 2'
    lxc query --wait /1.0/warnings\?recursion=1 | jq --exit-status 'length == 2'

    # Invalid query (unknown project)
    ! lxc query --wait -X POST -d '{"type_code": 0, "message": "global warning", "project": "foo"}' /internal/testing/warnings || false

    # Invalid query (unknown type code)
    ! lxc query --wait -X POST -d '{"type_code": 999, "message": "global warning"}' /internal/testing/warnings || false

    # Both entity type code as entity ID need to be valid otherwise no warning will be created. Note that empty/null values are valid as well.
    ! lxc query --wait -X POST -d '{"type_code": 0, "message": "global warning", "entity_type": "invalid_entity_type", "entity_id": 0}' /internal/testing/warnings || false

    ensure_import_testimage

    # Get image ID from database instead of assuming it
    image_id="$(lxd sql global --format csv 'SELECT image_id FROM images_aliases WHERE name="testimage"')"

    # Create a warning with entity type "image" and entity ID ${image_id} (the imported testimage)
    lxc query --wait -X POST -d '{"type_code": 0, "message": "global warning", "entity_type": "image", "entity_id": '"${image_id}"'}' /internal/testing/warnings

    # There should be three warnings now.
    lxc warning list --format json | jq --exit-status 'length == 3'

    # Test filtering
    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq new" | jq --exit-status '.metadata | length == 3'

    curl --silent --get --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq resolved" | jq --exit-status '.metadata == null'

    # Acknowledge a warning
    uuid="$(lxc warning list --format json | jq --exit-status --raw-output '.[] | select(.last_message=="global warning 2") | .uuid')"
    lxc warning ack "${uuid}"

    # This should hide the acknowledged
    lxc warning list --format json | jq --exit-status 'length == 2'

    # ... unless one uses --all.
    lxc warning list --all --format json | jq --exit-status 'length == 3'

    lxc warning show "${uuid}" | grep -xF "last_message: global warning 2"

    # Delete warning
    lxc warning rm "${uuid}"
    ! lxc warning list | grep -F "${uuid}" || false
    ! lxc warning show "${uuid}" || false

    # Delete all warnings
    lxc warning delete --all
    [ -z "$(lxc warning ls --format csv || echo fail)" ]
}
