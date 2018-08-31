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
    else
        ./easyrsa init-pki
        echo "lxd" | ./easyrsa build-ca nopass
        ./easyrsa build-server-full 127.0.0.1 nopass
        ./easyrsa build-client-full lxd-client nopass
        mkdir keys
        cp pki/private/* keys/
        cp pki/issued/* keys/
        cp pki/ca.crt keys/
    fi
  )

  # Setup the daemon
  LXD5_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD5_DIR}"
  cat "${TEST_DIR}/pki/keys/127.0.0.1.crt" "${TEST_DIR}/pki/keys/ca.crt" > "${LXD5_DIR}/server.crt"
  cp "${TEST_DIR}/pki/keys/127.0.0.1.key" "${LXD5_DIR}/server.key"
  cp "${TEST_DIR}/pki/keys/ca.crt" "${LXD5_DIR}/server.ca"
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
    lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=foo
    lxc_remote info pki-lxd:
  )

  # Confirm that a normal, non-PKI certificate doesn't
  ! lxc_remote remote add pki-lxd "${LXD5_ADDR}" --accept-certificate --password=foo

  kill_lxd "${LXD5_DIR}"
}
