test_oidc() {
  ensure_import_testimage

  # shellcheck disable=2153
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Setup OIDC
  spawn_oidc
  lxc config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
  lxc config set "oidc.client.id=device"

  BROWSER=curl lxc remote add --accept-certificate oidc "${LXD_ADDR}" --auth-type oidc
  [ "$(lxc info oidc: | grep ^auth_user_name | sed "s/.*: //g")" = "unknown" ]
  lxc remote remove oidc

  set_oidc test-user

  BROWSER=curl lxc remote add --accept-certificate oidc "${LXD_ADDR}" --auth-type oidc
  [ "$(lxc info oidc: | grep ^auth_user_name | sed "s/.*: //g")" = "test-user" ]
  lxc remote remove oidc

  # Cleanup OIDC
  kill_oidc
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id
}
