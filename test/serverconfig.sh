test_server_config() {

    export LXD_SERVERCONFIG_DIR=$(mktemp -d -p $(pwd))
    spawn_lxd 127.0.0.1:18450 $LXD_SERVERCONFIG_DIR

    lxc config set password 123456
    lxc config set core.foo value

    config=$(lxc config show)
    echo $config | grep -q "trust-password"
    echo $config | grep -q -v "123456"
    echo $config | grep -q "core.foo: value"

    # test untrusted server GET
    my_curl -X GET https://127.0.0.1:18450/1.0 | grep -v -q environment

}
