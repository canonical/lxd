test_oidc() {
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

  # Ensure a clean state before testing validation & rollback
  lxc config unset oidc.issuer
  lxc config unset oidc.client.id

  # Should be failed on wrong issuer.
  ! lxc config set oidc.issuer="http://127.0.0.1:22/" oidc.client.id="device" || false # Wrong port
  ! lxc config set "oidc.issuer=http://127.0.0.1:$(< "${TEST_DIR}/oidc.port")/wrong-path" "oidc.client.id=device" || false # Invalid path
  ! lxc config set "oidc.issuer=https://idp.example.com/" "oidc.client.id=device" || false # Invalid host


  # Should remain empty as above tests failed.
  [ -z "$(lxc config get oidc.client.id || echo fail)" ]
  [ -z "$(lxc config get oidc.issuer || echo fail)" ]

  lxc config set "oidc.issuer=http://127.0.0.1:$(< "${TEST_DIR}/oidc.port")/" "oidc.client.id=device" # Valid Configuration

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
  lxc query oidc:/1.0 | jq --exit-status '.auth == "trusted"'
  lxc query oidc:/1.0 | jq --exit-status '.auth_user_name == "test-user@example.com"'

  # OIDC user should be added to identities table.
  [ "$(lxd sql global --format csv "SELECT COUNT(*) FROM identities WHERE type = 5 AND identifier = 'test-user@example.com' AND auth_method = 2")" = 1 ]

  # A session should have been created.
  [ "$(lxc auth oidc-session list --format csv test-user@example.com | wc -l)" = 1 ]

  # A session cookie should be saved
  jq -e '."127.0.0.1"."127.0.0.1;/;session" | .Name == "session" and .Path == "/" and .Secure and .HttpOnly and .Persistent and .HostOnly and .Domain == "127.0.0.1" and .SameSite == "SameSite=Strict"' "${LXD_CONF}/jars/oidc"

  # Get the JWT payload.
  #
  # Here we're getting the value of the cookie, then getting the payload (middle section - JWTs have three base64
  # encoded sections delimited by a '.') and decoding it.
  #
  # If base64 -d returns an error we ignore it and pipe stderr to /dev/null, this is because the JWT does not contain
  # "=" padding at the end. When base64 encounters this, it returns what it was able to decode and an error.
  #
  # If any of this fails, the payload variable will be empty and the jq assertions below will fail.
  payload="$(jq -r '."127.0.0.1"."127.0.0.1;/;session".Value' "${LXD_CONF}/jars/oidc" | cut -d. -f2 | base64 -d 2>/dev/null || true)"

  cluster_uuid="$(lxc config get volatile.uuid)"
  session_uuid="$(lxc auth oidc-session list --format csv test-user@example.com | cut -d, -f3)"
  jq -e --arg cluster_uuid "${cluster_uuid}" --arg session_uuid "${session_uuid}" '.iss == "lxd:"+$cluster_uuid and .sub == $session_uuid and (.aud | length) == 1 and .aud[0] == "lxd:"+$cluster_uuid' <<< "${payload}"

  # Cleanup OIDC
  lxc auth identity delete oidc/test-user@example.com
  lxc remote remove oidc
  lxc config set oidc.issuer="" oidc.client.id=""
  kill_oidc
}
