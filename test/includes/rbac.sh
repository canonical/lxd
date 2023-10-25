# Test helper for external authentication

start_rbac_daemon() {

    (
        cd rbac || return
        # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
        go build -v -buildvcs=false ./...
    )
    # shellcheck disable=SC2039,3043
    local credentials_file tcp_port
    credentials_file="$1/macaroon-identity-credentials.csv"
    tcp_port="$(local_tcp_port)"
    cat <<EOF >"$credentials_file"
user1,pass1
user2,pass2
EOF

    endpoint="$(hostname -I | cut -d' ' -f1):${tcp_port}"
    rbac/rbac -endpoint "${endpoint}" -creds "$credentials_file" &
    set +x
    echo $! > "${TEST_DIR}/rbac.pid"
    echo "${endpoint}" > "${TEST_DIR}/rbac.addr"
}

kill_rbac_daemon() {
    # shellcheck disable=SC2039,3043
    local pidfile="$1/rbac.pid"
    kill "$(cat "$pidfile")" || true
    rm -f rbac/rbac
}
