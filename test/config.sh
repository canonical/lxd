test_config_profiles() {
  if ! lxc image alias list | grep -q "^| testimage\s*|.*$"; then
      if [ -e "$LXD_TEST_IMAGE" ]; then
          lxc image import $LXD_TEST_IMAGE --alias testimage
      else
          ../scripts/lxd-images import busybox --alias testimage
      fi
  fi
  lxc init testimage foo
  lxc profile list | grep default

  # let's check that 'lxc config profile' still works while it's deprecated
  lxc config profile list | grep default

  lxc config device add foo home disk source=/mnt path=/mnt readonly=true
  lxc profile create onenic
  lxc profile device add onenic eth0 nic nictype=bridged parent=lxcbr0
  lxc profile apply foo onenic
  lxc profile create unconfined
  lxc profile set unconfined raw.lxc "lxc.aa_profile=unconfined"
  lxc profile apply foo onenic,unconfined

  lxc config device list foo | grep home
  lxc config device show foo | grep "/mnt"
  lxc config show foo | grep "onenic,unconfined"
  lxc profile list | grep onenic
  lxc profile device list onenic | grep eth0
  lxc profile device show onenic | grep lxcbr0

  lxc config set foo user.prop value
  lxc list user.prop=value | grep foo
  lxc config unset foo user.prop

  bad=0
  lxc list user.prop=value | grep foo && bad=1
  if [ "$bad" -eq 1 ]; then
    echo "property unset failed"
  fi

  bad=0
  lxc config set foo user.prop 2>/dev/null && bad=1
  if [ "$bad" -eq 1 ]; then
    echo "property set succeded when it shouldn't have"
  fi

  lxc delete foo

  # Anything below this will not get run inside Travis-CI
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  lxc init testimage foo
  lxc profile apply foo onenic,unconfined
  lxc start foo

  lxc exec foo -- cat /proc/self/attr/current | grep unconfined
  lxc exec foo -- ls /sys/class/net | grep eth0

  lxc stop foo --force
  lxc delete foo
}
