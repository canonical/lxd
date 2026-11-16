test_remote_url() {
  # shellcheck disable=2153
  for url in "${LXD_ADDR}" "https://${LXD_ADDR}"; do
    lxc_remote remote add test "${url}" --accept-certificate --password foo
    lxc_remote info test:
    lxc_remote config trust list | grep @ | awk '{print $2}' | while read -r line ; do
      lxc_remote config trust remove "\"${line}\""
    done
    lxc_remote remote remove test
  done

  # shellcheck disable=2153
  urls="${LXD_DIR}/unix.socket unix:${LXD_DIR}/unix.socket unix://${LXD_DIR}/unix.socket"
  if [ -z "${LXD_OFFLINE:-}" ]; then
    urls="images.lxd.canonical.com https://images.lxd.canonical.com ${urls}"
  fi

  # an invalid protocol returns an error
  ! lxc_remote remote add test "${url}" --accept-certificate --password foo --protocol foo || false

  for url in ${urls}; do
    lxc_remote remote add test "${url}"
    lxc_remote remote remove test
  done

  # Test certificate chain handling with trust tokens and authentication.

  # Generate an untrusted client leaf and an arbitrary target certificate.
  gen_cert_and_key "${LXD_CONF}/chain_leaf.key" "${LXD_CONF}/chain_leaf.crt" "chain_leaf.local"
  gen_cert_and_key "${LXD_CONF}/chain_target.key" "${LXD_CONF}/chain_target.crt" "chain_target.local"
  leaf_fingerprint="$(cert_fingerprint "${LXD_CONF}/chain_leaf.crt")"
  leaf_fingerprint_short=$(echo "${leaf_fingerprint}" | cut -c1-12)
  target_fingerprint="$(cert_fingerprint "${LXD_CONF}/chain_target.crt")"

  # Build a multi-certificate chain: [leaf, target].
  cat "${LXD_CONF}/chain_leaf.crt" "${LXD_CONF}/chain_target.crt" > "${TEST_DIR}/chain_injected.pem"

  # Attempt onboarding using the multi-certificate chain.
  # The client proves ownership of chain_leaf.key during the TLS handshake.
  curl -k -s \
    --cert "${TEST_DIR}/chain_injected.pem" \
    --key "${LXD_CONF}/chain_leaf.key" \
    -X POST \
    -H "Content-Type: application/json" \
    --data "{\"password\": \"foo\", \"type\": \"client\", \"name\": \"chain-user\"}" \
    "https://${LXD_ADDR}/1.0/certificates" | jq --exit-status '.type == "sync" and .status_code == 200'

  # Assert that the activated identity matches the LEAF certificate, NOT the injected secondary certificate.
  lxc config trust list --format csv | grep -F "${leaf_fingerprint_short},chain_leaf.local"
  ! lxc config trust list --format csv | grep -F "$(echo "${target_fingerprint}" | cut -c1-12)" || false

  # Assert that the client presenting the chain signed by chain_leaf.key is now authenticated.
  curl -k -s \
    --cert "${TEST_DIR}/chain_injected.pem" \
    --key "${LXD_CONF}/chain_leaf.key" \
    "https://${LXD_ADDR}/1.0" | jq --exit-status '.metadata.auth == "trusted"'

  # Assert that a client using chain_target.key is NOT authenticated (it was never trusted).
  curl -k -s \
    --cert "${LXD_CONF}/chain_target.crt" \
    --key "${LXD_CONF}/chain_target.key" \
    "https://${LXD_ADDR}/1.0" | jq --exit-status '.metadata.auth == "untrusted"'

  # Assert that an untrusted leaf bundling an already trusted certificate cannot authenticate.
  # An attacker generates their own keypair and bundles the newly trusted 'chain_leaf.crt' into their chain.
  gen_cert_and_key "${LXD_CONF}/untrusted_attacker.key" "${LXD_CONF}/untrusted_attacker.crt" "untrusted_attacker.local"
  cat "${LXD_CONF}/untrusted_attacker.crt" "${LXD_CONF}/chain_leaf.crt" > "${TEST_DIR}/spoofed_chain.pem"

  curl -k -s \
    --cert "${TEST_DIR}/spoofed_chain.pem" \
    --key "${LXD_CONF}/untrusted_attacker.key" \
    "https://${LXD_ADDR}/1.0" | jq --exit-status '.metadata.auth == "untrusted"'

  # Clean up identity and temporary certificates.
  lxc config trust remove "${leaf_fingerprint}"
  rm "${TEST_DIR}/chain_injected.pem" "${TEST_DIR}/spoofed_chain.pem"
  rm "${LXD_CONF}/chain_leaf.crt" "${LXD_CONF}/chain_leaf.key" "${LXD_CONF}/chain_target.crt" "${LXD_CONF}/chain_target.key" "${LXD_CONF}/untrusted_attacker.crt" "${LXD_CONF}/untrusted_attacker.key"
}

test_remote_admin() {
  ! lxc_remote remote add badpass "${LXD_ADDR}" --accept-certificate --password bad || false
  ! lxc_remote list badpass: || false

  lxc_remote remote add foo "${LXD_ADDR}" --accept-certificate --password foo
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

  # This is a test for #91, we expect this to block asking for a password if we
  # tried to re-add our cert.
  echo y | lxc_remote remote add foo "${LXD_ADDR}"
  lxc_remote remote remove foo

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_cert client2

  # Test for #623
  lxc_remote remote add test-623 "${LXD_ADDR}" --accept-certificate --password foo
  lxc_remote remote remove test-623

  # now re-add under a different alias
  lxc_remote config trust add "${LXD_CONF}/client2.crt"
  if [ "$(lxc_remote config trust list | wc -l)" -ne 7 ]; then
    echo "wrong number of certs"
    false
  fi

  # Check that we can add domains with valid certs without confirmation:
  if [ -z "${LXD_OFFLINE:-}" ]; then
    lxc_remote remote add images1 images.lxd.canonical.com
    lxc_remote remote add images2 images.lxd.canonical.com:443
    lxc_remote remote remove images1
    lxc_remote remote remove images2
  fi
}

test_remote_usage() {
  # shellcheck disable=SC2039,SC3043
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc_remote remote add lxd2 "${LXD2_ADDR}" --accept-certificate --password foo

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

  # Skip the truly remote servers in offline mode.  There should always be
  # Ubuntu images in the results for the remote servers.
  if [ -z "${LXD_OFFLINE:-}" ]; then
    lxc_remote image list images: | grep -i -c ubuntu
    lxc_remote image list ubuntu: | grep -i -c ubuntu
  fi

  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"

  lxc_remote remote remove lxd2
  lxc_remote remote remove lxd2-public

  kill_lxd "$LXD2_DIR"
}
