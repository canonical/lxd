#!/bin/sh

gen_second_cert() {
  [ -f "${LXD_CONF}/client2.crt" ] && return
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client.crt.bak"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client.key.bak"
  lxc list > /dev/null 2>&1
  mv "${LXD_CONF}/client.crt" "${LXD_CONF}/client2.crt"
  mv "${LXD_CONF}/client.key" "${LXD_CONF}/client2.key"
  mv "${LXD_CONF}/client.crt.bak" "${LXD_CONF}/client.crt"
  mv "${LXD_CONF}/client.key.bak" "${LXD_CONF}/client.key"
}

test_remote_url() {
  for url in "${LXD_ADDR}" "https://${LXD_ADDR}"; do
    lxc remote add test "${url}" --accept-certificate --password foo
    lxc finger test:
    lxc config trust list | grep @ | awk '{print $2}' | while read line ; do
      lxc config trust remove "\"${line}\""
    done
    lxc remote remove test
  done

  urls="${LXD_DIR}/unix.socket unix:${LXD_DIR}/unix.socket unix://${LXD_DIR}/unix.socket"
  if [ -z "${LXD_TEST_DRACONIAN_PROXY:-}" ]; then
    urls="images.linuxcontainers.org https://images.linuxcontainers.org ${urls}"
  fi

  for url in ${urls}; do
    lxc remote add test "${url}"
    lxc finger test:
    lxc remote remove test
  done
}

test_remote_admin() {
  lxc remote add badpass "${LXD_ADDR}" --accept-certificate --password bad || true
  ! lxc list badpass:

  lxc remote add localhost "${LXD_ADDR}" --accept-certificate --password foo
  lxc remote list | grep 'localhost'

  lxc remote set-default localhost
  [ "$(lxc remote get-default)" = "localhost" ]

  lxc remote rename localhost foo
  lxc remote list | grep 'foo'
  lxc remote list | grep -v 'localhost'
  [ "$(lxc remote get-default)" = "foo" ]

  ! lxc remote remove foo
  lxc remote set-default local
  lxc remote remove foo

  # This is a test for #91, we expect this to hang asking for a password if we
  # tried to re-add our cert.
  echo y | lxc remote add localhost "${LXD_ADDR}"

  # we just re-add our cert under a different name to test the cert
  # manipulation mechanism.
  gen_second_cert

  # Test for #623
  lxc remote add test-623 "${LXD_ADDR}" --accept-certificate --password foo

  # now re-add under a different alias
  lxc config trust add "${LXD_CONF}/client2.crt"
  if [ "$(lxc config trust list | wc -l)" -ne 6 ]; then
    echo "wrong number of certs"
  fi

  # Check that we can add domains with valid certs without confirmation:

  # avoid default high port behind some proxies:
  if [ -z "${LXD_TEST_DRACONIAN_PROXY:-}" ]; then
    lxc remote add images images.linuxcontainers.org
    lxc remote add images2 images.linuxcontainers.org:443
  fi
}

test_remote_usage() {
  lxc remote add lxd2 "${LXD2_ADDR}" --accept-certificate --password foo

  # we need a public image on localhost
  lxc image export localhost:testimage "${LXD_DIR}/foo.img"
  lxc image delete localhost:testimage
  sum=$(sha256sum "${LXD_DIR}/foo.img" | cut -d' ' -f1)
  lxc image import "${LXD_DIR}/foo.img" localhost: --public
  lxc image alias create localhost:testimage "${sum}"

  lxc image delete "lxd2:${sum}" || true

  lxc image copy localhost:testimage lxd2: --copy-aliases --public
  lxc image delete "localhost:${sum}"
  lxc image copy "lxd2:${sum}" local: --copy-aliases --public
  lxc image info localhost:testimage
  lxc image delete "lxd2:${sum}"

  lxc image copy "localhost:${sum}" lxd2:
  lxc image delete "lxd2:${sum}"

  lxc image copy "localhost:$(echo "${sum}" | colrm 3)" lxd2:
  lxc image delete "lxd2:${sum}"

  # test a private image
  lxc image copy "localhost:${sum}" lxd2:
  lxc image delete "localhost:${sum}"
  lxc init "lxd2:${sum}" localhost:c1
  lxc delete localhost:c1

  lxc image alias create localhost:testimage "${sum}"

  # test remote publish
  lxc init testimage pub
  lxc publish pub lxd2: --alias bar --public a=b
  lxc image show lxd2:bar | grep -q "a: b"
  lxc image show lxd2:bar | grep -q "public: true"
  ! lxc image show bar
  lxc delete pub
  lxc image delete lxd2:bar

  # Double launch to test if the image downloads only once.
  lxc init localhost:testimage lxd2:c1 &
  C1PID=$!

  lxc init localhost:testimage lxd2:c2
  lxc delete lxd2:c2

  wait "${C1PID}"
  lxc delete lxd2:c1

  # launch testimage stored on localhost as container c1 on lxd2
  lxc launch localhost:testimage lxd2:c1

  # make sure it is running
  lxc list lxd2: | grep c1 | grep RUNNING
  lxc info lxd2:c1
  lxc stop lxd2:c1 --force
  lxc delete lxd2:c1
}
