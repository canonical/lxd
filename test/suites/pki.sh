test_pki() {
  if [ ! -d "/usr/share/easy-rsa/" ]; then
    echo "==> SKIP: The pki test requires easy-rsa to be installed"
    return
  fi

  # Setup the PKI
  cp -R /usr/share/easy-rsa "${TEST_DIR}/pki"
  (
    set -e
    cd "${TEST_DIR}/pki"
    # shellcheck disable=SC1091
    if [ -e pkitool ]; then
        . ./vars
        ./clean-all
        ./pkitool --initca
        ./pkitool --server 127.0.0.1
        ./pkitool lxd-client
        ./pkitool lxd-client-revoked
        # This will revoke the certificate but fail in the end as it tries to then verify the
        # revoked certificate.
        ./revoke-full lxd-client-revoked || true
    else
        ./easyrsa init-pki
        echo "lxd" | ./easyrsa build-ca nopass
        ./easyrsa gen-crl
        ./easyrsa build-server-full 127.0.0.1 nopass
        ./easyrsa build-client-full lxd-client nopass
        ./easyrsa build-client-full lxd-client-revoked nopass
        mkdir keys
        cp pki/private/* keys/
        cp pki/issued/* keys/
        cp pki/ca.crt keys/
        echo "yes" | ./easyrsa revoke lxd-client-revoked
        ./easyrsa gen-crl
        cp pki/crl.pem keys/
    fi
  )

  # Setup the daemon
  LXD5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD5_DIR}"
  cat "${TEST_DIR}/pki/keys/127.0.0.1.crt" "${TEST_DIR}/pki/keys/ca.crt" > "${LXD5_DIR}/server.crt"
  cp "${TEST_DIR}/pki/keys/127.0.0.1.key" "${LXD5_DIR}/server.key"
  cp "${TEST_DIR}/pki/keys/ca.crt" "${LXD5_DIR}/server.ca"
  cp "${TEST_DIR}/pki/keys/crl.pem" "${LXD5_DIR}/ca.crl"
  spawn_lxd "${LXD5_DIR}" true
  LXD5_ADDR=$(cat "${LXD5_DIR}/lxd.addr")

  # Setup the client
  LXC5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  cp "${TEST_DIR}/pki/keys/lxd-client.crt" "${LXC5_DIR}/client.crt"
  cp "${TEST_DIR}/pki/keys/lxd-client.key" "${LXC5_DIR}/client.key"
  cp "${TEST_DIR}/pki/keys/ca.crt" "${LXC5_DIR}/client.ca"

  # Confirm that a valid client certificate works
  (
    set -e
    export LXD_CONF=${LXC5_DIR}

    # Try adding remote using an incorrect password
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=bar || false

    # Add remote using the correct password
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=foo
    lxc_remote info pki-lxd:
    lxc_remote remote remove pki-lxd

    # Add remote using a CA-signed client certificate, and not providing a password
    LXD_DIR=${LXD5_DIR} lxc config set core.trust_ca_certificates true
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate
    lxc_remote info pki-lxd:
    lxc_remote remote remove pki-lxd

    # Add remote using a CA-signed client certificate, and providing an incorrect password
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=bar
    lxc_remote info pki-lxd:
    lxc_remote remote remove pki-lxd

    cp "${TEST_DIR}/pki/keys/lxd-client-revoked.crt" "${LXC5_DIR}/client.crt"
    cp "${TEST_DIR}/pki/keys/lxd-client-revoked.key" "${LXC5_DIR}/client.key"

    # Try adding a remote using a revoked client certificate, and the correct password
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=foo

    # Try adding a remote using a revoked client certificate, and an incorrect password
    ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=bar || false
  )

  # Confirm that a normal, non-PKI certificate doesn't
  ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=foo || false

  kill_lxd "${LXD5_DIR}"
}
