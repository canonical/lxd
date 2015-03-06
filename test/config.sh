test_config_profiles() {
  lxc init testimage foo

  lxc config device add foo home disk source=/mnt path=/mnt readonly=true
  lxc config profile create onenic
  lxc config profile device add onenic eth0 nic nictype=bridged parent=lxcbr0
  lxc config profile apply foo onenic
  lxc config profile create unconfined
  lxc config profile set unconfined raw.lxc "lxc.aa_profile=unconfined"
  lxc config profile apply foo onenic,unconfined

# FIXME: Broken
#  lxc config device list foo | grep home
#  lxc config profile show foo | grep "onenic,unconfined"
  lxc config profile list | grep onenic
  lxc config profile device list onenic | grep eth0

  lxc delete foo

  # Anything below this will not get run inside Travis-CI
  if [ -n "$TRAVIS_PULL_REQUEST" ]; then
    return
  fi

  lxc init testimage foo
  lxc start foo

  # Uncomment the below when the 'lxc()' define in main.sh works with
  # the --config
  #lxc exec foo -- cat /proc/self/attr/current | grep unconfined
  #lxc exec foo -- ls /sys/class/net | grep eth0

  lxc stop foo --force
  lxc delete foo
}
