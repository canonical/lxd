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

  # lxc info should show the email address as the username.
  [ "$(lxc info oidc: | grep ^auth_user_name | sed "s/.*: //g")" = "test-user@example.com" ]

  # OIDC user should be added to identities table.
  [ "$(lxd sql global "SELECT identifier, name, auth_method, type FROM identities WHERE type = 5 AND identifier = 'test-user@example.com' AND auth_method = 2" | wc -l)" = 5 ]

  # Cleanup OIDC
  lxc remote remove oidc
  kill_oidc
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}
