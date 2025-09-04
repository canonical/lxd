# shellcheck disable=2031
test_pki() {
  if [ ! -d "/usr/share/easy-rsa/" ]; then
    echo "==> SKIP: The pki test requires easy-rsa to be installed"
    return
  fi

  # Setup the PKI.
  cp -R /usr/share/easy-rsa "${TEST_DIR}/pki"
  (
    set -e
    cd "${TEST_DIR}/pki"

    cat << EOF > vars
set_var EASYRSA_ALGO ec
set_var EASYRSA_BATCH yes
EOF

    ./easyrsa init-pki
    echo "lxd" | ./easyrsa build-ca nopass
    ./easyrsa gen-crl
    ./easyrsa build-client-full restricted nopass
    ./easyrsa build-client-full unrestricted nopass
    ./easyrsa build-client-full fine-grained nopass
    ./easyrsa build-client-full ca-trusted nopass
    ./easyrsa build-client-full prior-revoked nopass
    mkdir keys
    cp pki/private/* keys/
    cp pki/issued/* keys/
    cp pki/ca.crt keys/
    echo "yes" | ./easyrsa revoke prior-revoked
    ./easyrsa gen-crl
    cp pki/crl.pem keys/
  )

  # Setup the daemon.
  LXD5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD5_DIR}" true
  LXD5_ADDR=$(< "${LXD5_DIR}/lxd.addr")

  # Add a certificate to the trust store that is not signed by the CA before enabling CA mode.
  token="$(LXD_DIR=${LXD5_DIR} lxc config trust add --name foo --quiet --project default)"
  lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}"

  cert_common_name="$(openssl x509 -noout -subject -in "${LXD_CONF}/client.crt" -nameopt multiline | awk -F' = ' '/commonName/ {print $2}')"
  LXD_DIR="${LXD5_DIR}" lxc config trust list --format csv | grep -F "client,foo,${cert_common_name},$(cert_fingerprint "${LXD_CONF}/client.crt" | cut -c1-12)"

  # Shutdown LXD. The CA certificate and revokation list must be present at start up to enable PKI.
  shutdown_lxd "${LXD5_DIR}"
  cp "${TEST_DIR}/pki/keys/ca.crt" "${LXD5_DIR}/server.ca"
  cp "${TEST_DIR}/pki/keys/crl.pem" "${LXD5_DIR}/ca.crl"
  respawn_lxd "${LXD5_DIR}" true

  # New tmp directory for lxc client config.
  LXC5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  # Confirm that a valid client certificate works.
  (
    set -e
    # shellcheck disable=2030
    export LXD_CONF="${LXC5_DIR}"
    export LXD_DIR="${LXD5_DIR}"

    ### Unrestricted CA signed client certificate with `core.trust_ca_certificates` disabled.

    # Set up the client config
    cp "${TEST_DIR}/pki/keys/unrestricted.crt" "${LXD_CONF}/client.crt"
    cp "${TEST_DIR}/pki/keys/unrestricted.key" "${LXD_CONF}/client.key"
    cp "${TEST_DIR}/pki/keys/ca.crt" "${LXD_CONF}/client.ca"
    cat "${LXD_CONF}/client.crt" "${LXD_CONF}/client.key" > "${LXD_CONF}/client.pem"
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"

    # Try adding remote using an incorrect token. This should fail even though the client certificate
    # has been signed by the CA because `core.trust_ca_certificates` is not enabled.
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token=bar || false

    # Add remote using the correct token.
    # This should work because the client certificate is signed by the CA.
    token="$(lxc config trust add --name bar -q)"
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}"

    # Should have trust store entry because `core.trust_ca_certificates` is disabled.
    lxc_remote config trust list pki-lxd: --format csv | grep -F "client,bar,unrestricted,$(printf '%.12s' "${fingerprint}")"
    [ "$(lxc config trust list --format csv | wc -l)" = 2 ]

    # The certificate was not restricted, so should be able to view server config
    lxc_remote info pki-lxd: | grep -F 'core.https_address'
    curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"'

    # Revoke the client certificate
    cd "${TEST_DIR}/pki" && "${TEST_DIR}/pki/easyrsa" --batch revoke unrestricted keyCompromise && "${TEST_DIR}/pki/easyrsa" gen-crl && cd -

    # Restart LXD with the revoked certificate in the CRL.
    shutdown_lxd "${LXD5_DIR}"
    cp "${TEST_DIR}/pki/pki/crl.pem" "${LXD5_DIR}/ca.crl"
    respawn_lxd "${LXD5_DIR}" true

    # Revoked certificate no longer has access even though it is in the trust store.
    lxc_remote info pki-lxd: | grep -F 'auth: untrusted'
    ! lxc_remote ls pki-lxd: || false
    [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]

    # Remove cert from truststore.
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"
    lxc config trust remove "${fingerprint}"
    lxc_remote remote remove pki-lxd
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    # The certificate is now revoked, we shouldn't be able to re-add it.
    token="$(lxc config trust add --name snap -q)"
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}" || false
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    ### Restricted CA signed client certificate with `core.trust_ca_certificates` disabled.

    # Set up the client config
    cp "${TEST_DIR}/pki/keys/restricted.crt" "${LXD_CONF}/client.crt"
    cp "${TEST_DIR}/pki/keys/restricted.key" "${LXD_CONF}/client.key"
    cat "${LXD_CONF}/client.crt" "${LXD_CONF}/client.key" > "${LXD_CONF}/client.pem"
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"

    # Try adding remote using an incorrect token. This should fail even though the client certificate
    # has been signed by the CA because `core.trust_ca_certificates` is not enabled.
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token=bar || false

    # Add remote using the correct token (restricted).
    # This should work because the client certificate is signed by the CA.
    token="$(lxc config trust add --name baz --quiet --restricted)"
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}"

    # Should have a trust store entry because `core.trust_ca_certificates` is disabled.
    lxc_remote config trust list pki-lxd: --format csv | grep -F "client,baz,restricted,$(printf '%.12s' "${fingerprint}")"
    [ "$(lxc config trust list --format csv | wc -l)" = 2 ]

    # The certificate was restricted, so should not be able to view server config
    ! lxc_remote info pki-lxd: | grep -F 'core.https_address' || false
    ! curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"' || false

    # Enable `core.trust_ca_certificates`.
    lxc config set core.trust_ca_certificates true

    # The certificate was restricted, so should not be able to view server config even though `core.trust_ca_certificates` is now enabled.
    ! lxc_remote info pki-lxd: | grep -F 'core.https_address' || false
    ! curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"' || false

    # Revoke the client certificate
    cd "${TEST_DIR}/pki" && "${TEST_DIR}/pki/easyrsa" --batch revoke restricted keyCompromise && "${TEST_DIR}/pki/easyrsa" gen-crl && cd -

    # Restart LXD with the revoked certificate in the CRL.
    shutdown_lxd "${LXD5_DIR}"
    cp "${TEST_DIR}/pki/pki/crl.pem" "${LXD5_DIR}/ca.crl"
    respawn_lxd "${LXD5_DIR}" true

    # Revoked certificate no longer has access even though it is in the trust store.
    lxc_remote info pki-lxd: | grep -F 'auth: untrusted'
    ! lxc_remote ls pki-lxd: || false
    [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]

    # Remove cert from truststore.
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"
    lxc config trust remove "${fingerprint}"
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]
    lxc_remote remote remove pki-lxd

    # Unset `core.trust_ca_certificates`.
    lxc config unset core.trust_ca_certificates

    # The certificate is now revoked, we shouldn't be able to re-add it.
    token="$(lxc config trust add --name crackle -q)"
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}" || false
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    ### Fine-grained TLS identity with `core.trust_ca_certificates` disabled.

    # Set up the client config
    cp "${TEST_DIR}/pki/keys/fine-grained.crt" "${LXD_CONF}/client.crt"
    cp "${TEST_DIR}/pki/keys/fine-grained.key" "${LXD_CONF}/client.key"
    cat "${LXD_CONF}/client.crt" "${LXD_CONF}/client.key" > "${LXD_CONF}/client.pem"
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"

    # Try adding remote using an incorrect token. This should fail even though the client certificate
    # has been signed by the CA because `core.trust_ca_certificates` is not enabled.
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token=bar || false

    # Add remote using the correct token.
    # This should work because the client certificate is signed by the CA.
    token="$(lxc auth identity create tls/fizz --quiet)"
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}"

    # Should be shown in `identity list` because `core.trust_ca_certificates` is disabled.
    lxc_remote auth identity list pki-lxd: --format csv | grep -F "tls,Client certificate,fizz,${fingerprint}"

    # Should not be shown `lxc config trust list` because it is a fine-grained identity that can't be managed via this subcommand.
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    # The identity is not a member of any groups, so should not be able to view server config
    ! lxc_remote info pki-lxd: | grep -F 'core.https_address' || false
    ! curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"' || false

    # Enable `core.trust_ca_certificates`.
    lxc config set core.trust_ca_certificates true

    # The identity is not a member of any groups, so should not be able to view server config even though `core.trust_ca_certificates` is now enabled.
    ! lxc_remote info pki-lxd: | grep -F 'core.https_address' || false
    ! curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"' || false

    # Revoke the client certificate
    cd "${TEST_DIR}/pki" && "${TEST_DIR}/pki/easyrsa" --batch revoke fine-grained keyCompromise && "${TEST_DIR}/pki/easyrsa" gen-crl && cd -

    # Restart LXD with the revoked certificate in the CRL.
    shutdown_lxd "${LXD5_DIR}"
    cp "${TEST_DIR}/pki/pki/crl.pem" "${LXD5_DIR}/ca.crl"
    respawn_lxd "${LXD5_DIR}" true

    # Revoked certificate no longer has access even though it is in the trust store.
    lxc_remote info pki-lxd: | grep -F 'auth: untrusted'
    ! lxc_remote ls pki-lxd: || false
    [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]

    # Remove cert from truststore.
    lxc auth identity delete "tls/${fingerprint}"
    lxc_remote remote remove pki-lxd

    # Unset `core.trust_ca_certificates`.
    lxc config unset core.trust_ca_certificates

    # The certificate is now revoked, we can create a pending identity.
    token="$(lxc auth identity create tls/buzz --quiet)"
    lxc auth identity list --format csv | grep -F 'tls,Client certificate (pending),buzz'
    # But adding the remote will not work
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}" || false
    # And the identity should still be pending.
    lxc auth identity list --format csv | grep -F 'tls,Client certificate (pending),buzz'

    # The pending TLS identity can be deleted
    lxc auth identity delete tls/buzz

    ### CA signed certificate with `core.trust_ca_certificates` enabled.

    # NOTE: These certificates cannot be restricted/unrestricted because no trust store entries are created for them.

    # Set up the client config
    cp "${TEST_DIR}/pki/keys/ca-trusted.crt" "${LXD_CONF}/client.crt"
    cp "${TEST_DIR}/pki/keys/ca-trusted.key" "${LXD_CONF}/client.key"
    cat "${LXD_CONF}/client.crt" "${LXD_CONF}/client.key" > "${LXD_CONF}/client.pem"
    fingerprint="$(cert_fingerprint "${LXD_CONF}/client.crt")"

    # Enable `core.trust_ca_certificates`.
    lxc config set core.trust_ca_certificates true

    # Add remote using a CA-signed client certificate, and not providing a token.
    # This should succeed because `core.trust_ca_certificates` is enabled.
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate

    # Client cert should not be present in trust store.
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    # The certificate is trusted as root because `core.trust_ca_certificates` is enabled.
    lxc_remote info pki-lxd: | grep -F 'core.https_address'
    curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"'

    # Unset `core.trust_ca_certificates` (this should work because the certificate is trusted as root as `core.trust_ca_certificates` is still enabled).
    lxc_remote config unset pki-lxd: core.trust_ca_certificates

    # Check that we no longer have access.
    lxc_remote info pki-lxd: | grep -F 'auth: untrusted'
    ! lxc_remote ls pki-lxd: || false
    [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]

    # Re-enable `core.trust_ca_certificates`.
    lxc config set core.trust_ca_certificates true

    # Revoke the client certificate
    cd "${TEST_DIR}/pki" && "${TEST_DIR}/pki/easyrsa" --batch revoke ca-trusted keyCompromise && "${TEST_DIR}/pki/easyrsa" gen-crl && cd -

    # Restart LXD with the revoked certificate.
    shutdown_lxd "${LXD5_DIR}"
    cp "${TEST_DIR}/pki/pki/crl.pem" "${LXD5_DIR}/ca.crl"
    respawn_lxd "${LXD5_DIR}" true

    # Check that we no longer have access (certificate was previously trusted, but is now revoked).
    lxc_remote info pki-lxd: | grep -F 'auth: untrusted'
    ! lxc_remote ls pki-lxd: || false
    [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]

    # Remove remote.
    lxc remote remove pki-lxd

    ### CA signed certificate that has been revoked prior to connecting to LXD.
    # `core.trust_ca_certificates` is currently enabled.

    # Replace the client certificate with a revoked certificate in the CRL.
    cp "${TEST_DIR}/pki/keys/prior-revoked.crt" "${LXC5_DIR}/client.crt"
    cp "${TEST_DIR}/pki/keys/prior-revoked.key" "${LXC5_DIR}/client.key"

    # Try adding a remote using a revoked client certificate, and the correct token.
    # This should fail, and the revoked certificate should not be added to the trust store.
    token="$(lxc config trust add --name spam -q)"
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}" || false
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    # Try adding a remote using a revoked client certificate, and an incorrect token.
    # This should fail, as if the certificate is revoked and token is wrong then no access should be allowed.
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token=incorrect || false

    # Unset `core.trust_ca_certificates` and re-test, there should be no change in behaviour as the certificate is revoked.
    lxc config unset core.trust_ca_certificates

    # Try adding a remote using a revoked client certificate, and the correct token.
    # This should fail, and the revoked certificate should not be added to the trust store.
    token="$(lxc config trust add --name ham -q)"
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}" || false
    [ "$(lxc config trust list --format csv | wc -l)" = 1 ]

    # The revoked certificate is not valid when an identity token is used either.
    token="$(lxc auth identity create tls/pop --quiet)"
    lxc auth identity list --format csv | grep -F 'tls,Client certificate (pending),pop'
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token "${token}" || false
    lxc auth identity list --format csv | grep -F 'tls,Client certificate (pending),pop'
    lxc auth identity delete tls/pop


    # Try adding a remote using a revoked client certificate, and an incorrect token.
    # This should fail, as if the certificate is revoked and token is wrong then no access should be allowed.
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --token=incorrect || false

    # Check we can't access anything with the revoked certificate.
    [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]
  )

  # Confirm that we cannot add a remote using a certificate that is not signed by the CA.
  # Outside of the subshell above, `LXD_CONF` is not set to `LXD5_DIR` where the CA trusted certs are.
  # Since we added a certificate to the trust store prior to enabling PKI, the certificates in current `LXD_CONF` are
  # in the trust store, but not signed by the CA. So here we are checking that mTLS for a client does not work when CA
  # mode is enabled.
  token="$(LXD_DIR=${LXD5_DIR} lxc config trust add --name foo -q)"
  ! lxc_remote remote add pki-lxd2 "${LXD5_ADDR}" --token "${token}" || false
  [ "$(LXD_DIR="${LXD5_DIR}" lxc config trust list --format csv | wc -l)" = 1 ]

  # Confirm that the certificate we added earlier cannot authenticate with LXD.
  lxc_remote info pki-lxd: | grep -F 'auth: untrusted'
  ! lxc_remote ls pki-lxd: || false
  cat "${LXD_CONF}/client.crt" "${LXD_CONF}/client.key" > "${LXD_CONF}/client.pem"
  [ "$(curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0/instances" | jq -e -r '.error')" = "Forbidden" ]


  ### Show that mTLS still works for server certificates:

  # Trick LXD into thinking the client cert is a server certificate.
  LXD_DIR="${LXD5_DIR}" lxd sql global "UPDATE identities SET type = 3 WHERE identifier = '$(cert_fingerprint "${LXD_CONF}/client.crt")'"
  LXD_DIR="${LXD5_DIR}" lxc query -X POST "/internal/identity-cache-refresh"

  # A server certificate should have root access, so we can see server configuration.
  lxc_remote info pki-lxd: | grep -F 'core.https_address'
  curl -s --cert "${LXD_CONF}/client.pem" --cacert "${LXD5_DIR}/server.crt" "https://${LXD5_ADDR}/1.0" | jq -e '.metadata.config."core.https_address"'

  # Clean up.

  rm "${LXD_CONF}/client.pem"
  lxc remote rm pki-lxd

  kill_lxd "${LXD5_DIR}"
}
