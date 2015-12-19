#!/bin/sh

test_image_expiry() {
  ensure_import_testimage

  if ! lxc_remote remote list | grep -q l1; then
    lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  fi
  if ! lxc_remote remote list | grep -q l2; then
    lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo
  fi
  lxc_remote init l1:testimage l2:c1
  fp=$(lxc_remote image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }')
  [ ! -z "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-10)

  lxc_remote image list l2: | grep -q "${fpbrief}"

  lxc_remote remote set-default l2
  lxc_remote config set images.remote_cache_expiry 0
  lxc_remote remote set-default local

  ! lxc_remote image list l2: | grep -q "${fpbrief}"

  lxc_remote delete l2:c1

  # rest the default expiry
  lxc_remote remote set-default l2
  lxc_remote config set images.remote_cache_expiry 10
  lxc_remote remote set-default local
}
