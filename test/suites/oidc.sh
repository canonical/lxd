test_oidc() {
  ensure_import_testimage

  # shellcheck disable=2153
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup OIDC
  spawn_oidc
  lxc config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
  lxc config set "oidc.client.id=device"

  # Expect this to fail. No user set.
  ! BROWSER=curl lxc remote add --accept-certificate oidc "${LXD_ADDR}" --auth-type oidc || false

  # Set a user with no email address
  set_oidc test-user

  # Expect this to fail. mini-oidc will issue a token but adding the remote will fail because no email address will be
  # returned from /userinfo
  ! BROWSER=curl lxc remote add --accept-certificate oidc "${LXD_ADDR}" --auth-type oidc || false

  # Set a user with an email address
  set_oidc test-user test-user@example.com

  # This should succeed.
  BROWSER=curl lxc remote add --accept-certificate oidc "${LXD_ADDR}" --auth-type oidc

  # The user should now be logged in and their email should show in the "auth_user_name" field.
  [ "$(lxc query oidc:/1.0 | jq -r '.auth')" = "trusted" ]
  [ "$(lxc query oidc:/1.0 | jq -r '.auth_user_name')" = "test-user@example.com" ]

  # OIDC user should be added to identities table.
  [ "$(lxd sql global --format csv "SELECT count(*) FROM identities WHERE type = 5 AND identifier = 'test-user@example.com' AND auth_method = 2")" = 1 ]

  # Cleanup OIDC
  lxc auth identity delete oidc/test-user@example.com
  lxc remote remove oidc
  kill_oidc
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}
