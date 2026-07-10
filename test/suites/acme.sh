test_acme() {
  # Start the fake ACME server with HTTP-01 validation against LXD.
  spawn_acme "${LXD_ADDR}"

  # Restart LXD with the LEGO_CA_CERTIFICATES variable set so that it
  # trusts the mini-acme CA certificate.
  shutdown_lxd "${LXD_DIR}"
  LEGO_CA_CERTIFICATES="${TEST_DIR}/mini-acme-ca.crt" respawn_lxd "${LXD_DIR}" true

  local ACME_DOMAIN="lxd$$.example.com"
  local ACME_PORT
  ACME_PORT="$(< "${TEST_DIR}/acme.port")"

  # Save the old certificate
  cp "${LXD_DIR}/server.crt" "${LXD_DIR}/server.crt.bak"

  sub_test "Set ACME configuration and trigger certificate renewal"
  lxc config set acme.agree_tos=true acme.ca_url="https://127.0.0.1:${ACME_PORT}/directory" acme.domain="${ACME_DOMAIN}" acme.email=coyote@acme.example.com

  sub_test "Verify LXD serves a certificate signed by the ACME CA"
  local LXD_PORT="${LXD_ADDR##*:}"
  curl -s --cacert "${TEST_DIR}/mini-acme-ca.crt" --resolve "${ACME_DOMAIN}:${LXD_PORT}:127.0.0.1" -o /dev/null "https://${ACME_DOMAIN}:${LXD_PORT}/"

  sub_test "Clear ACME configuration"
  lxc config set acme.agree_tos= acme.ca_url="" acme.domain="" acme.email=""

  # Cleanup.
  kill_acme

  shutdown_lxd "${LXD_DIR}"
  mv "${LXD_DIR}/server.crt.bak" "${LXD_DIR}/server.crt"
  respawn_lxd "${LXD_DIR}" true
}
