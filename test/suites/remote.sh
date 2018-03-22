test_remote_url() {
  # shellcheck disable=2153
  for url in "${LXD_ADDR}" "https://${LXD_ADDR}"; do
    lxc_remote remote add test "${url}" --accept-certificate --password foo
    lxc_remote info test:
    lxc_remote config trust list | grep @ | awk '{print $2}' | while read -r line ; do
      lxc_remote config trust remove "\"${line}\""
    done
    lxc_remote remote remove test
  done

  # shellcheck disable=2153
  urls="${LXD_DIR}/unix.socket unix:${LXD_DIR}/unix.socket unix://${LXD_DIR}/unix.socket"
  if [ -z "${LXD_OFFLINE:-}" ]; then
    urls="images.linuxcontainers.org https://images.linuxcontainers.org ${urls}"
  fi

  # an invalid protocol returns an error
  ! lxc_remote remote add test "${url}" --accept-certificate --password foo --protocol foo

  for url in ${urls}; do
    lxc_remote remote add test "${url}"
    lxc_remote remote remove test
  done
}

test_remote_admin() {
  lxc_remote remote add badpass "${LXD_ADDR}" --accept-certificate --password bad || true
  ! lxc_remote list badpass:

  lxc_remote remote add localhost "${LXD_ADDR}" --accept-certificate --password foo
  lxc_remote remote list | grep 'localhost'

  lxc_remote remote set-default localhost
  [ "$(lxc_remote remote get-default)" = "localhost" ]

  lxc_remote remote rename localhost foo
  lxc_remote remote list | grep 'foo'
  lxc_remote remote list | grep -v 'localhost'
  [ "$(lxc_remote remote get-default)" = "foo" ]

  ! lxc_remote remote remove foo
  lxc_remote remote set-default local
  lxc_remote remote remove foo

  # This is a test for #91, we expect this to hang asking for a password if we
  # tried to re-add our cert.
  echo y | lxc_remote remote add localhost "${LXD_ADDR}"

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_cert client2

  # Test for #623
  lxc_remote remote add test-623 "${LXD_ADDR}" --accept-certificate --password foo

  # now re-add under a different alias
  lxc_remote config trust add "${LXD_CONF}/client2.crt"
  if [ "$(lxc_remote config trust list | wc -l)" -ne 7 ]; then
    echo "wrong number of certs"
    false
  fi

  # Check that we can add domains with valid certs without confirmation:

  # avoid default high port behind some proxies:
  if [ -z "${LXD_OFFLINE:-}" ]; then
    lxc_remote remote add images1 images.linuxcontainers.org
    lxc_remote remote add images2 images.linuxcontainers.org:443
  fi
}

test_remote_usage() {
  # shellcheck disable=2039
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc_remote remote add lxd2 "${LXD2_ADDR}" --accept-certificate --password foo

  # we need a public image on localhost

  lxc_remote image export localhost:testimage "${LXD_DIR}/foo"
  lxc_remote image delete localhost:testimage
  sum=$(sha256sum "${LXD_DIR}/foo.tar.xz" | cut -d' ' -f1)
  lxc_remote image import "${LXD_DIR}/foo.tar.xz" localhost: --public
  lxc_remote image alias create localhost:testimage "${sum}"

  lxc_remote image delete "lxd2:${sum}" || true

  lxc_remote image copy localhost:testimage lxd2: --copy-aliases --public
  lxc_remote image delete "localhost:${sum}"
  lxc_remote image copy "lxd2:${sum}" local: --copy-aliases --public
  lxc_remote image info localhost:testimage
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:${sum}" lxd2:
  lxc_remote image delete "lxd2:${sum}"

  lxc_remote image copy "localhost:$(echo "${sum}" | colrm 3)" lxd2:
  lxc_remote image delete "lxd2:${sum}"

  # test a private image
  lxc_remote image copy "localhost:${sum}" lxd2:
  lxc_remote image delete "localhost:${sum}"
  lxc_remote init "lxd2:${sum}" localhost:c1
  lxc_remote delete localhost:c1

  lxc_remote image alias create localhost:testimage "${sum}"

  # test remote publish
  lxc_remote init testimage pub
  lxc_remote publish pub lxd2: --alias bar --public a=b
  lxc_remote image show lxd2:bar | grep -q "a: b"
  lxc_remote image show lxd2:bar | grep -q "public: true"
  ! lxc_remote image show bar
  lxc_remote delete pub

  # test spawn from public server
  lxc_remote remote add lxd2-public "${LXD2_ADDR}" --public --accept-certificate
  lxc_remote init lxd2-public:bar pub
  lxc_remote image delete lxd2:bar
  lxc_remote delete pub

  # Double launch to test if the image downloads only once.
  lxc_remote init localhost:testimage lxd2:c1 &
  C1PID=$!

  lxc_remote init localhost:testimage lxd2:c2
  lxc_remote delete lxd2:c2

  wait "${C1PID}"
  lxc_remote delete lxd2:c1

  # launch testimage stored on localhost as container c1 on lxd2
  lxc_remote launch localhost:testimage lxd2:c1

  # make sure it is running
  lxc_remote list lxd2: | grep c1 | grep RUNNING
  lxc_remote info lxd2:c1
  lxc_remote stop lxd2:c1 --force
  lxc_remote delete lxd2:c1

  # Test that local and public servers can be accessed without a client cert
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"

  # testimage should still exist on the local server.
  lxc_remote image list local: | grep -q testimage

  # Skip the truly remote servers in offline mode.  There should always be
  # Ubuntu images in the results for the remote servers.
  if [ -z "${LXD_OFFLINE:-}" ]; then
    lxc_remote image list images: | grep -i -c ubuntu
    lxc_remote image list ubuntu: | grep -i -c ubuntu
  fi

  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"

  kill_lxd "$LXD2_DIR"
}
