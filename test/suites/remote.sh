test_remote_url() {
  # Add remotes by setting the token explicitly.
  # shellcheck disable=2153
  for url in "${LXD_ADDR}" "https://${LXD_ADDR}"; do
    token="$(lxc config trust add --name foo -q)"
    lxc_remote remote add test "${url}" --token "${token}"
    lxc_remote info test:
    for fingerprint in $(lxc_remote config trust list -f csv | awk -F, '{print $4}'); do
      lxc_remote config trust remove "${fingerprint}"
    done
    lxc_remote remote remove test
  done

  # invalid certificate names returns an error
  ! lxc_remote config trust add --name -foo || false
  ! lxc_remote config trust add --name fo/o || false

  # shellcheck disable=2153
  urls="${LXD_DIR}/unix.socket unix:${LXD_DIR}/unix.socket unix://${LXD_DIR}/unix.socket"

  # an invalid protocol returns an error
  ! lxc_remote remote add test "${url}" --protocol foo || false
  [ "$(CLIENT_DEBUG="" SHELL_TRACING="" lxc_remote remote add test "${url}" --protocol foo 2>&1)" = "Error: Invalid protocol: foo" ]

  for url in ${urls}; do
    lxc_remote remote add test "${url}"
    lxc_remote remote remove test
  done

  # Check that we can add simplestream remotes with valid certs without confirmation
  if curl --head --silent https://cloud-images.ubuntu.com/releases/ > /dev/null; then
    lxc_remote remote add ubuntu1 https://cloud-images.ubuntu.com/releases/ --protocol=simplestreams
    lxc_remote remote add ubuntu2 https://cloud-images.ubuntu.com:443/releases/ --protocol=simplestreams
    lxc_remote remote remove ubuntu1
    lxc_remote remote remove ubuntu2
  fi
}

test_remote_url_with_token() {
  # Try adding remote using a correctly constructed but invalid token
  invalid_token="eyJjbGllbnRfbmFtZSI6IiIsImZpbmdlcnByaW50IjoiMWM0MmMzOTgxOWIyNGJiYjQxNGFhYTY2NDUwNzlmZGY2NDQ4MTUzMDcxNjA0YTFjODJjMjVhN2JhNjBkZmViMCIsImFkZHJlc3NlcyI6WyIxOTIuMTY4LjE3OC4yNDo4NDQzIiwiWzIwMDM6Zjc6MzcxMToyMzAwOmQ5ZmY6NWRiMDo3ZTA2OmQ1ODldOjg0NDMiLCIxMC41OC4yLjE6ODQ0MyIsIjEwLjAuMy4xOjg0NDMiLCIxOTIuMTY4LjE3OC43MDo4NDQzIiwiWzIwMDM6Zjc6MzcxMToyMzAwOjQwMTY6ODVkNDo2M2FlOjNhYWVdOjg0NDMiLCIxMC4xMjQuODYuMTo4NDQzIiwiW2ZkNDI6ZTY5Zjo3OTczOjIyMjU6OjFdOjg0NDMiXSwic2VjcmV0IjoiODVlMGU5YmViODk0ZTFhMTU3YmYxODI4YTk0Y2IwYTdjY2YxMzQ4NzMyN2ZjMTY3MDcyY2JlNjQ3NmVmOGJkMiJ9"
  ! lxc_remote remote add test "${invalid_token}" || false

  # Generate token for client foo
  echo foo | lxc config trust add -q

  # Listing all tokens should show only a single one
  [ "$(lxc config trust list-tokens -f json | jq '[.[] | select(.ClientName == "foo")] |  length')" -eq 1 ]

  # Extract token
  token="$(lxc config trust list-tokens -f json | jq '.[].Token')"

  # Invalidate token so that it cannot be used again
  lxc config trust revoke-token foo

  # Ensure the token is invalidated
  [ "$(lxc config trust list-tokens -f json | jq 'length')" -eq 0 ]

  # Try adding the remote using the invalidated token
  ! lxc_remote remote add test "${token}" || false

  # Generate token for client foo
  lxc project create foo
  echo foo | lxc config trust add -q --projects foo --restricted

  # Extract the token
  token="$(lxc config trust list-tokens -f json | jq -r '.[].Token')"

  # Add the valid token
  lxc_remote remote add test "${token}"

  # Ensure the token is invalidated
  [ "$(lxc config trust list-tokens -f json | jq -r 'length')" -eq 0 ]

  # List instances as the remote has been added
  lxc_remote ls test:

  # Clean up
  lxc_remote remote remove test
  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  echo foo | lxc config trust add -q

  # Extract token
  token="$(lxc config trust list-tokens -f json | jq -r '.[].Token')"

  # create new certificate
  gen_cert_and_key "token-client"

  # Try accessing instances (this should fail)
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances" | jq -r '.error_code')" -eq 403 ]

  # Add valid token
  CERTNAME="token-client" my_curl -X POST --fail-with-body -H 'Content-Type: application/json' -d '{"trust_token": "'"${token}"'"}' "https://${LXD_ADDR}/1.0/certificates"

  # Check if we can see instances
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances" | jq -r '.status_code')" -eq 200 ]

  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  echo foo | lxc config trust add -q --projects foo --restricted

  # Extract token
  token="$(lxc config trust list-tokens -f json | jq -r '.[].Token')"

  # Ensure there is a default expiry set (expressed in UTC)
  expiresAt="$(lxc config trust list-tokens -f json | jq --exit-status --raw-output '.[].ExpiresAt')"
  [[ "${expiresAt}" =~ UTC$ ]]

  # Add valid token but override projects
  CERTNAME="token-client" my_curl -X POST --fail-with-body -H 'Content-Type: application/json' -d '{"trust_token": "'"${token}"'","projects":["default","foo"],"restricted":true}' "https://${LXD_ADDR}/1.0/certificates"

  # Check if we can see instances in the foo project
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances?project=foo" | jq -r '.status_code')" -eq 200 ]

  # Check if we can see instances in the default project (this should fail)
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances" | jq -r '.error_code')" -eq 403 ]

  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Set token expiry to 1 seconds
  lxc config set core.remote_token_expiry 1S

  # Generate new token
  token="$(lxc config trust add --name foo --quiet)"

  # Try adding remote. This should succeed.
  lxc_remote remote add test "${token}"

  # Remove all trusted clients
  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Remove remote
  lxc_remote remote rm test

  # Generate new token
  token="$(lxc config trust add --name foo --quiet)"

  # This will cause the token to expire
  sleep 1.1

  # Try adding remote. This should fail.
  ! lxc_remote remote add test "${token}" || false

  # Check token prune task
  lxc config trust add --name foo --quiet # Create a token
  [ "$(lxc operation list --format csv | grep -cF 'TOKEN,Certificate add token,RUNNING')" -eq 1 ] # Expect only one token operation to be running
  running_token_operation_uuid="$(lxc operation list --format csv | grep -F 'TOKEN,Certificate add token,RUNNING' | cut -d, -f1)" # Get the operation UUID
  sleep 1.1 # Wait for token to expire (expiry still set to short expiry)
  lxc query --request POST /internal/testing/prune-tokens # Prune tokens
  [ "$(lxc query "/1.0/operations/${running_token_operation_uuid}" | jq -r '.status')" = "Cancelled" ] # Expect the operation to be cancelled

  # Unset token expiry
  lxc config unset core.remote_token_expiry

  # Delete project
  lxc project delete foo
}

test_remote_admin() {
  echo "Verify error due to bad token and inspect error message"
  OUTPUT="$(! lxc_remote remote add badtoken "${LXD_ADDR}" --token badtoken 2>&1 || false)"
  echo "${OUTPUT}" | grep -F "Error: Failed decoding trust token:"

  echo "Verify that a bad token does not succeed in adding remote"
  ! lxc_remote remote add badtoken "${LXD_ADDR}" --token badtoken || false
  if lxc_remote remote list | grep -wF badtoken; then
    echo "Remote added with bad token"
    false
  fi

  token="$(lxc config trust add --name foo -q)"

  # Ensure trust token cannot be used with --accept-certificate.
  ! lxc_remote remote add foo "${LXD_ADDR}" --accept-certificate --token "${token}" || false

  lxc_remote remote add foo "${LXD_ADDR}" --token "${token}"
  lxc_remote remote list | grep -wF 'foo'

  lxc_remote remote set-default foo
  [ "$(lxc_remote remote get-default)" = "foo" ]

  lxc_remote remote rename foo bar
  lxc_remote remote list -f csv | grep '^bar'
  if lxc_remote remote list -f csv | grep '^foo'; then
    echo "Remote rename failed, old name still exists"
    false
  fi
  [ "$(lxc_remote remote get-default)" = "bar" ]

  ! lxc_remote remote remove bar || false
  lxc_remote remote set-default local
  lxc_remote remote remove bar

  # This is a test for #91, we expect this to block asking for a token if we
  # tried to re-add our cert.
  echo y | lxc_remote remote add foo "${LXD_ADDR}"
  lxc_remote remote remove foo

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_cert_and_key client2

  # Test for #623
  token="$(lxc config trust add --name foo -q)"
  lxc_remote remote add test-623 "${LXD_ADDR}" --token "${token}"
  lxc_remote remote remove test-623

  # now re-add under a different alias
  lxc_remote config trust add "${LXD_CONF}/client2.crt"
  if [ "$(lxc_remote config trust list | wc -l)" -ne 7 ]; then
    echo "wrong number of certs"
    false
  fi
}

test_remote_usage() {
  # Remove any leftover localhost remote from prior tests
  lxc remote remove localhost || true

  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add lxd2 "${LXD2_ADDR}" --token "${token}"

  # Publish a local container to the same server with custom properties.
  sub_test "Verify local publish"
  lxc_remote init --quiet testimage pub
  lxc_remote publish --quiet pub local: --alias bar --public a=b
  lxc_remote image show local:bar | grep -F "a: b"
  lxc_remote image show local:bar | grep -xF "public: true"
  lxc_remote delete pub
  lxc_remote image delete local:bar

  # Cross-remote publish is not supported with image registries.
  sub_test "Verify cross-remote publish is rejected"
  lxc_remote init --quiet testimage pub
  ! lxc_remote publish --quiet pub lxd2: --alias bar --public || false
  lxc_remote delete pub

  # Verify that the local server can be accessed without a client certificate
  # via the unix socket.
  sub_test "Verify local image access without client certificate"
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  lxc_remote image list local: | grep -wF testimage
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"

  # Verify that push and relay modes are rejected when image registries are supported.
  sub_test "Verify push and relay modes are rejected"
  ! lxc_remote image copy --quiet testimage lxd2: --mode=push || false
  ! lxc_remote image copy --quiet testimage lxd2: --mode=relay || false

  # Copy an image between projects on the same server.
  sub_test "Verify same-server cross-project image copy"
  lxc_remote project create localhost:p1
  lxc_remote image copy --quiet testimage localhost: --project default --target-project p1 --copy-aliases
  lxc_remote image list localhost: --project p1 | grep -wF testimage

  # Clean up.
  lxc_remote image delete localhost:testimage --project p1
  lxc_remote project delete localhost:p1
  lxc_remote remote remove lxd2

  kill_lxd "$LXD2_DIR"
}
