test_server_config() {
  LXD_SERVERCONFIG_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_SERVERCONFIG_DIR}" true

  ensure_has_localhost_remote "${LXD_ADDR}"
  lxc config set core.trust_password 123456

  config=$(lxc config show)
  echo "${config}" | grep -q "trust_password"
  echo "${config}" | grep -q -v "123456"

  lxc config unset core.trust_password
  lxc config show | grep -q -v "trust_password"

  # test untrusted server GET
  my_curl -X GET "https://$(cat "${LXD_SERVERCONFIG_DIR}/lxd.addr")/1.0" | grep -v -q environment

  # test authentication type
  curl --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq .metadata.auth_methods | grep tls
  # only tls is enabled by default
  ! curl --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq .metadata.auth_methods | grep macaroons
  lxc config set core.macaroon.endpoint "https://localhost:8081"
  # macaroons are also enabled
  curl --unix-socket "$LXD_DIR/unix.socket" "lxd/1.0" | jq .metadata.auth_methods | grep macaroons
  lxc config unset core.macaroon.endpoint

  kill_lxd "${LXD_SERVERCONFIG_DIR}"
}
