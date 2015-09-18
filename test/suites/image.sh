test_image_expiry() {
  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
    if [ -e "$LXD_TEST_IMAGE" ]; then
      lxc image import $LXD_TEST_IMAGE --alias testimage
    else
      ../scripts/lxd-images import busybox --alias testimage
    fi
  fi

  if ! lxc remote list | grep -q l1; then
    (echo y;  sleep 3;  echo foo) | lxc remote add l1 127.0.0.1:18443 $debug
  fi
  if ! lxc remote list | grep -q l2; then
    (echo y;  sleep 3;  echo foo) | lxc remote add l2 127.0.0.1:18444 $debug
  fi
  lxc init l1:testimage l2:c1
  fp=`lxc image info testimage | awk -F: '/^Fingerprint/ { print $2 }' | awk '{ print $1 }'`
  [ ! -z "$fp" ]
  fpbrief=`echo $fp | cut -c 1-10`

  lxc image list l2: | grep -q $fpbrief

  lxc remote set-default l2
  lxc config set images.remote_cache_expiry 0
  sed -i '/^default-remote:/d' ${LXD_CONF}/config.yml

  bad=0
  lxc image list l2: | grep -q $fpbrief && bad=1 || true
  if [ $bad -eq 1 ]; then
    echo "expired image was not removed"; false
  fi

  lxc delete l2:c1

  # rest the default expiry
  lxc remote set-default l2
  lxc config set images.remote_cache_expiry 10
  sed -i '/^default-remote:/d' ${LXD_CONF}/config.yml
}
