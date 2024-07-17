test_remote_url() {
  # Add remotes by setting the token explicitly.
  # shellcheck disable=2153
  for url in "${LXD_ADDR}" "https://${LXD_ADDR}"; do
    token="$(lxc config trust add --name foo -q)"
    lxc_remote remote add test "${url}" --accept-certificate --token "${token}"
    lxc_remote info test:
    lxc_remote config trust list | awk '/@/ {print $8}' | while read -r line ; do
      lxc_remote config trust remove "\"${line}\""
    done
    lxc_remote remote remove test
  done

  # shellcheck disable=2153
  urls="${LXD_DIR}/unix.socket unix:${LXD_DIR}/unix.socket unix://${LXD_DIR}/unix.socket"

  # an invalid protocol returns an error
  ! lxc_remote remote add test "${url}" --accept-certificate --token foo --protocol foo || false

  for url in ${urls}; do
    lxc_remote remote add test "${url}"
    lxc_remote remote remove test
  done

  # Check that we can add simplestream remotes with valid certs without confirmation
  if [ -z "${LXD_OFFLINE:-}" ]; then
    lxc_remote remote add ubuntu1 https://cloud-images.ubuntu.com/releases --protocol=simplestreams
    lxc_remote remote add ubuntu2 https://cloud-images.ubuntu.com:443/releases --protocol=simplestreams
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
  [ "$(lxc config trust list-tokens -f json | jq 'length')" -eq 0 ]

  # List instances as the remote has been added
  lxc_remote ls test:

  # Clean up
  lxc_remote remote remove test
  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  echo foo | lxc config trust add -q

  # Extract token
  token="$(lxc config trust list-tokens -f json | jq '.[].Token')"

  # create new certificate
  gen_cert_and_key "token-client"

  # Try accessing instances (this should fail)
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances" | jq '.error_code')" -eq 403 ]

  # Add valid token
  CERTNAME="token-client" my_curl -X POST -d "{\"trust_token\": ${token}}" "https://${LXD_ADDR}/1.0/certificates"

  # Check if we can see instances
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances" | jq '.status_code')" -eq 200 ]

  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Generate new token
  echo foo | lxc config trust add -q --projects foo --restricted

  # Extract token
  token="$(lxc config trust list-tokens -f json | jq '.[].Token')"

  # Add valid token but override projects
  CERTNAME="token-client" my_curl -X POST -d "{\"trust_token\":${token},\"projects\":[\"default\",\"foo\"],\"restricted\":false}" "https://${LXD_ADDR}/1.0/certificates"

  # Check if we can see instances in the foo project
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances?project=foo" | jq '.status_code')" -eq 200 ]

  # Check if we can see instances in the default project (this should fail)
  [ "$(CERTNAME="token-client" my_curl "https://${LXD_ADDR}/1.0/instances" | jq '.error_code')" -eq 403 ]

  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Set token expiry to 1 seconds
  lxc config set core.remote_token_expiry 1S

  # Generate new token
  token="$(lxc config trust add --name foo | tail -n1)"

  # Try adding remote. This should succeed.
  lxc_remote remote add test "${token}"

  # Remove all trusted clients
  lxc config trust rm "$(lxc config trust list -f json | jq -r '.[].fingerprint')"

  # Remove remote
  lxc_remote remote rm test

  # Generate new token
  token="$(lxc config trust add --name foo | tail -n1)"

  # This will cause the token to expire
  sleep 2

  # Try adding remote. This should fail.
  ! lxc_remote remote add test "${token}" || false

  # Unset token expiry
  lxc config unset core.remote_token_expiry

  # Delete project
  lxc project delete foo
}

test_remote_admin() {
  ! lxc_remote remote add badpass "${LXD_ADDR}" --accept-certificate --token badtoken || false
  ! lxc_remote list badpass: || false

  token="$(lxc config trust add --name foo -q)"
  lxc_remote remote add foo "${LXD_ADDR}" --accept-certificate --token "${token}"
  lxc_remote remote list | grep 'foo'

  lxc_remote remote set-default foo
  [ "$(lxc_remote remote get-default)" = "foo" ]

  lxc_remote remote rename foo bar
  lxc_remote remote list | grep 'bar'
  lxc_remote remote list | grep -v 'foo'
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
  gen_cert client2

  # Test for #623
  token="$(lxc config trust add --name foo -q)"
  lxc_remote remote add test-623 "${LXD_ADDR}" --accept-certificate --token "${token}"
  lxc_remote remote remove test-623

  # now re-add under a different alias
  lxc_remote config trust add "${LXD_CONF}/client2.crt"
  if [ "$(lxc_remote config trust list | wc -l)" -ne 7 ]; then
    echo "wrong number of certs"
    false
  fi
}

test_remote_usage() {
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add lxd2 "${LXD2_ADDR}" --accept-certificate --token "${token}"

  # we need a public image on localhost

  lxc_remote image export localhost:testimage "${LXD_DIR}/foo"
  lxc_remote image delete localhost:testimage
  sum=$(sha256sum "${LXD_DIR}/foo.tar.xz" | cut -d' ' -f1)
  lxc_remote image import "${LXD_DIR}/foo.tar.xz" localhost: --public
  lxc_remote image alias create localhost:testimage "${sum}"

  lxc_remote image delete "lxd2:${sum}" || true

  lxc_remote image copy localhost:testimage lxd2: --copy-aliases --public
  lxc_remote image delete "localhost:${sum}"
  lxc_remote image copy "lxd2:${sum}" local: --copy-aliases --public
  lxc_remote image info localhost:testimage
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2:
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:$(echo "${sum}" | colrm 3)" lxd2:
  lxc_remote image delete "lxd2:${sum}"

  # test a private image
  lxc_remote image copy "localhost:${sum}" lxd2:
  lxc_remote image delete "localhost:${sum}"
  lxc_remote init "lxd2:${sum}" localhost:c1
  lxc_remote delete localhost:c1

  lxc_remote image alias create localhost:testimage "${sum}"

  # test remote publish
  lxc_remote init testimage pub
  lxc_remote publish pub lxd2: --alias bar --public a=b
  lxc_remote image show lxd2:bar | grep -q "a: b"
  lxc_remote image show lxd2:bar | grep -q "public: true"
  ! lxc_remote image show bar || false
  lxc_remote delete pub

  # test spawn from public server
  lxc_remote remote add lxd2-public "${LXD2_ADDR}" --public --accept-certificate
  lxc_remote init lxd2-public:bar pub
  lxc_remote image delete lxd2:bar
  lxc_remote delete pub

  # Double launch to test if the image downloads only once.
  lxc_remote init localhost:testimage lxd2:c1 &
  C1PID=$!

  lxc_remote init localhost:testimage lxd2:c2
  lxc_remote delete lxd2:c2

  wait "${C1PID}"
  lxc_remote delete lxd2:c1

  # launch testimage stored on localhost as container c1 on lxd2
  lxc_remote launch localhost:testimage lxd2:c1

  # make sure it is running
  lxc_remote list lxd2: | grep c1 | grep RUNNING
  lxc_remote info lxd2:c1
  lxc_remote stop lxd2:c1 --force
  lxc_remote delete lxd2:c1

  # Test that local and public servers can be accessed without a client cert
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"

  # testimage should still exist on the local server.
  lxc_remote image list local: | grep -q testimage

  # Skip the truly remote servers in offline mode.
  # There should always be Ubuntu images in the results from cloud-images.ubuntu.com remote.
  # And test for alpine in the images.lxd.canonical.com remote.
  if [ -z "${LXD_OFFLINE:-}" ]; then
    lxc_remote image list images: | grep -i -c alpine
    lxc_remote image list ubuntu: | grep -i -c ubuntu
  fi

  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"

  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image alias create localhost:foo "${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --mode=push
  lxc_remote image show lxd2:"${sum}"
  lxc_remote image show lxd2:"${sum}" | grep -q 'public: false'
  ! lxc_remote image show lxd2:foo || false
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --mode=push --copy-aliases --public
  lxc_remote image show lxd2:"${sum}"
  lxc_remote image show lxd2:"${sum}" | grep -q 'public: true'
  lxc_remote image show lxd2:foo
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --mode=push --copy-aliases --alias=bar
  lxc_remote image show lxd2:"${sum}"
  lxc_remote image show lxd2:foo
  lxc_remote image show lxd2:bar
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --mode=relay
  lxc_remote image show lxd2:"${sum}"
  lxc_remote image show lxd2:"${sum}" | grep -q 'public: false'
  ! lxc_remote image show lxd2:foo || false
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --mode=relay --copy-aliases --public
  lxc_remote image show lxd2:"${sum}"
  lxc_remote image show lxd2:"${sum}" | grep -q 'public: true'
  lxc_remote image show lxd2:foo
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --mode=relay --copy-aliases --alias=bar
  lxc_remote image show lxd2:"${sum}"
  lxc_remote image show lxd2:foo
  lxc_remote image show lxd2:bar
  lxc_remote image delete "lxd2:${sum}"

  # Test image copy between projects
  lxc_remote project create lxd2:foo
  lxc_remote image copy "localhost:${sum}" lxd2: --target-project foo
  lxc_remote image show lxd2:"${sum}" --project foo
  lxc_remote image delete "lxd2:${sum}" --project foo
  lxc_remote image copy "localhost:${sum}" lxd2: --target-project foo --mode=push
  lxc_remote image show lxd2:"${sum}" --project foo
  lxc_remote image delete "lxd2:${sum}" --project foo
  lxc_remote image copy "localhost:${sum}" lxd2: --target-project foo --mode=relay
  lxc_remote image show lxd2:"${sum}" --project foo
  lxc_remote image delete "lxd2:${sum}" --project foo
  lxc_remote project delete lxd2:foo

  # Test image copy with --profile option
  lxc_remote profile create lxd2:foo
  lxc_remote image copy "localhost:${sum}" lxd2: --profile foo
  lxc_remote image show lxd2:"${sum}" | grep -q '\- foo'
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --profile foo --mode=push
  lxc_remote image show lxd2:"${sum}" | grep -q '\- foo'
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2: --profile foo --mode=relay
  lxc_remote image show lxd2:"${sum}" | grep -q '\- foo'
  lxc_remote image delete "lxd2:${sum}"
  lxc_remote profile delete lxd2:foo

  lxc_remote image copy localhost:testimage lxd2: --alias bar
  # Get the `cached` and `aliases` fields for the image `bar` in lxd2
  cached=$(lxc_remote image info lxd2:bar | awk '/Cached/ { print $2 }')
  alias=$(lxc_remote image info lxd2:bar | grep -A 1 "Aliases:" | tail -n1 | awk '{print $2}')

  # Check that image is not cached
  [ "${cached}" = "no" ]
  # Check that the alias is correct
  [ "${alias}" = "bar" ]

  # Now, lets delete the image and observe that when its downloaded implicitly as part of an instance create,
  # the image becomes `cached` and has no alias.
  fingerprint=$(lxc_remote image info lxd2:bar | awk '/Fingerprint/ { print $2 }')
  lxc_remote image delete lxd2:bar
  lxc_remote init localhost:testimage lxd2:c1
  cached=$(lxc_remote image info "lxd2:${fingerprint}" | awk '/Cached/ { print $2 }')
  # The `cached` field should be set to `yes` since the image was implicitly downloaded by the instance create operation
  [ "${cached}" = "yes" ]
  # There should be no alias for the image
  ! lxc_remote image info "lxd2:${fingerprint}" | grep -q "Aliases:"

  # Finally, lets copy the remote image explicitly to the local server with an alias like we did before
  lxc_remote image copy localhost:testimage lxd2: --alias bar
  cached=$(lxc_remote image info lxd2:bar | awk '/Cached/ { print $2 }')
  alias=$(lxc_remote image info lxd2:bar | grep -A 1 "Aliases:" | tail -n1 | awk '{print $2}')
  # The `cached` field should be set to `no` since the image was explicitly copied.
  [ "${cached}" = "no" ]
  # The alias should be set to `bar`.
  [ "${alias}" = "bar" ]

  lxc_remote image alias delete localhost:foo

  lxc_remote remote remove lxd2
  lxc_remote remote remove lxd2-public

  kill_lxd "$LXD2_DIR"
}
