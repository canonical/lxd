test_oidc() {
  ensure_import_testimage

  # shellcheck disable=2153
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Check OIDC scopes validation
  ! lxc config set oidc.scopes "my-scope" || false # Doesn't contain "email" or "openid"
  ! lxc config set oidc.scopes "my-scope email" || false # Doesn't contain "openid"
  ! lxc config set oidc.scopes "my-scope openid" || false # Doesn't contain "email"

  lxc config set oidc.scopes "my-scope email openid" # Valid
  lxc config unset oidc.scopes # Should reset to include profile and offline access claims

  # Setup OIDC
  spawn_oidc
  lxc config set "oidc.issuer=http://127.0.0.1:$(< "${TEST_DIR}/oidc.port")/"
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
  [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM identities WHERE type = 5 AND identifier = 'test-user@example.com' AND auth_method = 2")" = 1 ]

  # Cleanup OIDC
  lxc auth identity delete oidc/test-user@example.com
  lxc remote remove oidc
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}
