complete() {
    # Can't use lxc function as it adds other arguments (like the --verbose flag).
    cmd=$(unset -f lxc; command -v lxc)

    # Run the completion and remove the last line (the directive), then sort and convert to csv.
    "${cmd}" __complete "${@}" 2>/dev/null | head -n -1 | sort | paste -sd,
}

completion_directive() {
  # Can't use lxc function as it adds other arguments (like the --verbose flag).
  cmd=$(unset -f lxc; command -v lxc)

  # Run the completion and get the last line (the directive).
  "${cmd}" __complete "${@}" 2>/dev/null | tail -n 1
}

test_completions() {
    ensure_import_testimage
    ensure_has_localhost_remote "${LXD_ADDR}"

    # 'remote'
    [ "$(complete remote remove '')" = 'localhost' ] # Only non-static remotes may be removed
    [ "$(complete remote remove u)" = '' ]
    [ "$(complete remote remove l)" = 'localhost' ]
    [ "$(complete remote rename '')" = 'localhost' ] # Only non-static remotes may be renamed
    [ "$(complete remote rename l)" = 'localhost' ]
    [ "$(complete remote set-url '')" = 'localhost' ] # Only URLs of non-static remotes may be changed
    [ "$(complete remote set-url l)" = 'localhost' ]
    [ "$(complete remote switch '')" = 'localhost' ] # Only private remotes suggested
    [ "$(complete remote switch u)" = '' ]
    [ "$(complete image list '')" = 'images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ] # Image list should show all remotes except the default
    [ "$(complete image list i)" = 'images:' ]
    [ "$(complete list '')" = 'localhost:' ] # Instance list should show only instance server remotes and not the default
    [ "$(complete list l)" = 'localhost:' ]
    lxc remote switch localhost
    [ "$(complete remote remove '')" = '' ] # Can't remove current remote or any static remotes, so no valid suggestions
    [ "$(complete remote rename '')" = 'localhost' ] # Only non-static remotes may be renamed
    [ "$(complete remote set-url '')" = 'localhost' ] # Only URLs of non-static remotes may be changed
    [ "$(complete remote set-url l)" = 'localhost' ]
    [ "$(complete remote switch '')" = 'local' ] # Only private remotes suggested
    [ "$(complete remote switch u)" = '' ]
    [ "$(complete image list '')" = 'images:,local:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ] # Image list should show all remotes except the default
    [ "$(complete image list i)" = 'images:' ]
    [ "$(complete list '')" = 'local:' ] # Instance list should show only instance server remotes and not the default
    [ "$(complete list l)" = 'local:' ]
    lxc remote switch local

    # top level (instance) commands
    lxc init --empty c1
    lxc launch testimage c2
    lxc snapshot c1
    [ "$(complete console '')" = 'c1,c2,localhost:' ] # Console should show only instance server remotes and not the default
    [ "$(complete console l)" = 'localhost:' ]
    [ "$(complete console c)" = 'c1,c2' ]
    [ "$(complete console localhost:c)" = 'localhost:c1,localhost:c2' ]
    [ "$(complete copy '')" = 'c1,c2,localhost:' ] # Copy should show only instance server remotes and not the default
    [ "$(complete copy l)" = 'localhost:' ]
    [ "$(complete copy c)" = 'c1,c2' ]
    [ "$(complete copy localhost:c)" = 'localhost:c1,localhost:c2' ]
    [ "$(complete delete '')" = 'c1,localhost:' ] # Delete should show only instance server remotes and not the default, c2 should not be shown as it is not running
    [ "$(complete delete l)" = 'localhost:' ]
    [ "$(complete delete c)" = 'c1' ]
    [ "$(complete delete --force '')" = 'c1,c2,localhost:' ] # Delete should show only instance server remotes and not the default, c2 should be shown as the --force flag was passed
    [ "$(complete delete --force l)" = 'localhost:' ]
    [ "$(complete delete --force c)" = 'c1,c2' ]
    [ "$(complete delete --force localhost:c)" = 'localhost:c1,localhost:c2' ]
    [ "$(complete exec '')" = 'c2,localhost:' ] # Exec should show only instance server remotes and not the default, c1 should not be shown because it is not running.
    [ "$(complete exec l)" = 'localhost:' ]
    [ "$(complete exec c)" = 'c2' ]
    [ "$(complete exec localhost:c)" = 'localhost:c2' ]
    [ "$(complete export '')" = 'c1,c2,localhost:' ] # Export should show only instance server remotes and not the default
    [ "$(complete export l)" = 'localhost:' ]
    [ "$(complete export c)" = 'c1,c2' ]

    (
      import_wd=$(mktemp -d -p "${TEST_DIR}" XXX)
      cd "${import_wd}" || return 1
      mkdir foo
      touch bar.txt fizz.tar buzz.tar.gz bazz.tar.xz
      [ "$(complete import '')" = 'bazz.tar.xz,buzz.tar.gz,foo/,localhost:' ] # Correct file extensions, directories, and non-default remotes
      [ "$(complete import localhost: '')" = 'bazz.tar.xz,buzz.tar.gz,foo/' ]
      rm -rf "${import_wd}"
    )

    [ "$(complete info '')" = 'c1,c2,localhost:' ] # Info should show only instance server remotes and not the default
    [ "$(complete info l)" = 'localhost:' ]
    [ "$(complete info c)" = 'c1,c2' ]
    [ "$(complete init '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ] # Init should show images from the default remote, and all image remotes
    [ "$(complete init l)" = 'localhost:' ]
    [ "$(complete launch '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ] # Launch should show images from the default remote, and all image remotes
    [ "$(complete launch l)" = 'localhost:' ]
    [ "$(complete init testimage '')" = "localhost:" ] # Second argument to init should show only instance remotes
    [ "$(complete init testimage l)" = 'localhost:' ]
    [ "$(complete launch testimage '')" = "localhost:" ] # Second argument to launch should show only instance remotes
    [ "$(complete launch testimage l)" = 'localhost:' ]
    [ "$(complete monitor '')" = 'localhost:' ] # Monitor should show only instance server remotes and not the default
    [ "$(complete monitor l)" = 'localhost:' ]
    [ "$(complete move '')" = 'c1,c2,localhost:' ] # Move should show only instance server remotes and not the default
    [ "$(complete move l)" = 'localhost:' ]
    [ "$(complete move c)" = 'c1,c2' ]
    [ "$(complete pause '')" = 'c2,localhost:' ] # Only c2 running
    [ "$(complete publish '')" = 'c1,c2,localhost:' ]
    [ "$(complete publish l)" = 'localhost:' ]
    [ "$(complete publish c)" = 'c1,c2' ]
    [ "$(complete publish c1/s)" = 'c1/snap0' ]
    [ "$(complete publish c1/)" = 'c1/snap0' ]
    [ "$(complete rebuild '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete rebuild u)" = "ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete rebuild testimage '')" = 'c1,c2,localhost:' ]
    [ "$(complete rebuild testimage l)" = 'localhost:' ]
    [ "$(complete rebuild testimage c)" = 'c1,c2' ]
    [ "$(complete rename '')" = 'c1,c2,localhost:' ]
    [ "$(complete rename l)" = 'localhost:' ]
    [ "$(complete rename c)" = 'c1,c2' ]
    [ "$(complete rename c1/)" = 'c1/snap0' ]
    [ "$(complete restore '')" = 'c1,c2,localhost:' ]
    [ "$(complete restore c)" = 'c1,c2' ]
    [ "$(complete restore l)" = 'localhost:' ]
    [ "$(complete restore c1 '')" = 'snap0' ]
    [ "$(complete restore c2 '')" = '' ]
    [ "$(complete snapshot '')" = 'c1,c2,localhost:' ]
    [ "$(complete snapshot l)" = 'localhost:' ]
    [ "$(complete snapshot c)" = 'c1,c2' ]
    [ "$(complete start '')" = 'c1,localhost:' ] # Only c1 not running
    [ "$(complete stop '')" = 'c2,localhost:' ] # Only c2 running

    # 'file'
    [ "$(complete file create '')" = 'c1/,c2/,localhost:' ]
    [ "$(complete file delete '')" = 'c1/,c2/,localhost:' ]
    [ "$(complete file edit '')" = 'c1/,c2/,localhost:' ]
    [ "$(complete file mount '')" = 'c1/,c2/,localhost:' ]
    [ "$(completion_directive file mount c1 '')" = ':0' ] # Default directive for local files.
    [ "$(complete file pull '')" = 'c1/,c2/,localhost:' ]
    (
      file_wd=$(mktemp -d -p "${TEST_DIR}" XXX)
      cd "${file_wd}" || return 1
      mkdir foo
      touch bar.txt
      [ "$(complete file pull c1/foo '')" = 'bar.txt,c1/,c2/,foo/,localhost:' ]
      [ "$(complete file pull c1/foo c2/foo '')" = 'bar.txt,c1/,c2/,foo/,localhost:' ]
      [ "$(completion_directive file push '')" = ':0' ] # Default directive for local files.
      [ "$(complete file push foo '')" = "bar.txt,c1/,c2/,foo/,localhost:" ]
      rm -rf "${file_wd}"
    )

    # 'config'
    [ "$(complete config show '')" = 'c1,c2,localhost:' ]
    [ "$(complete config set '')" = 'acme.,backups.,c1,c2,cluster.,core.,images.,instances.,localhost:,loki.,maas.,network.,oidc.,storage.,user.' ]
    [ "$(complete config set n)" = 'network.' ]
    [ "$(complete config set c)" = 'c1,c2,cluster.,core.' ]
    [ "$(complete config set l)" = 'localhost:,loki.' ]
    [ "$(complete config set localhost: m)" = 'maas.' ]
    [ "$(complete config set localhost: maas.)" = 'maas.api.,maas.machine=' ]
    [ "$(complete config set localhost: maas.api.)" = 'maas.api.key=,maas.api.url=' ]
    [ "$(complete config set c1 '')" = 'boot.,cloud-init.,cluster.,environment.,limits.,linux.,migration.,nvidia.,raw.,security.,snapshots.,ubuntu_pro.,user.' ]
    [ "$(complete config set c1 l)" = 'limits.,linux.' ]
    [ "$(complete config set localhost:c1 '')" = 'boot.,cloud-init.,cluster.,environment.,limits.,linux.,migration.,nvidia.,raw.,security.,snapshots.,ubuntu_pro.,user.' ]
    [ "$(complete config set c1 limits.)" = 'limits.cpu.,limits.cpu=,limits.disk.,limits.hugepages.,limits.kernel.,limits.memory.,limits.memory=,limits.processes=' ]
    [ "$(complete config set c1 migration.)" = 'migration.incremental.' ] # No .stateful because c1 is not a VM.
    [ "$(complete config get '')" = 'acme.,backups.,c1,c2,cluster.,core.,images.,instances.,localhost:,loki.,maas.,network.,oidc.,storage.,user.' ]
    [ "$(complete config get n)" = 'network.' ]
    [ "$(complete config get c)" = 'c1,c2,cluster.,core.' ]
    [ "$(complete config get l)" = 'localhost:,loki.' ]
    [ "$(complete config get localhost: m)" = 'maas.' ]
    [ "$(complete config get localhost: maas.)" = 'maas.api.,maas.machine' ]
    [ "$(complete config get localhost: maas.api.)" = 'maas.api.key,maas.api.url' ]
    [ "$(complete config get c1 '')" = 'boot.,cloud-init.,cluster.,environment.,limits.,linux.,migration.,nvidia.,raw.,security.,snapshots.,ubuntu_pro.,user.' ]
    [ "$(complete config get c1 l)" = 'limits.,linux.' ]
    [ "$(complete config get localhost:c1 '')" = 'boot.,cloud-init.,cluster.,environment.,limits.,linux.,migration.,nvidia.,raw.,security.,snapshots.,ubuntu_pro.,user.' ]
    [ "$(complete config get c1 limits.)" = 'limits.cpu,limits.cpu.,limits.disk.,limits.hugepages.,limits.kernel.,limits.memory,limits.memory.,limits.processes' ]
    lxc config set user.foo=bar
    [ "$(complete config unset '')" = 'c1,c2,core.https_address,localhost:,user.foo' ]
    [ "$(complete config unset c)" = 'c1,c2,core.https_address' ]
    [ "$(complete config unset l)" = 'localhost:' ]
    [ "$(complete config unset localhost: '')" = 'core.https_address,user.foo' ]
    [ "$(complete config unset localhost:c)" = 'localhost:c1,localhost:c2' ]
    [ "$(complete config unset localhost: c)" = 'core.https_address' ]
    [ "$(complete config unset c1 '')" = '' ]
    lxc config set c1 user.foo=bar
    [ "$(complete config unset c1 '')" = 'user.foo' ]
    [ "$(complete config unset localhost:c1 '')" = 'user.foo' ]
    lxc config unset c1 user.foo
    lxc config unset user.foo

    # 'config device'
    [ "$(complete config device add '')" = 'c1,c2,localhost:' ]
    [ "$(complete config device add c)" = 'c1,c2' ]
    [ "$(complete config device add l)" = 'localhost:' ]
    [ "$(complete config device add localhost:)" = 'localhost:c1,localhost:c2' ]
    [ "$(complete config device add c1 devname '')" = 'disk,gpu,infiniband,nic,pci,proxy,tpm,unix-block,unix-char,unix-hotplug,usb' ]
    [ "$(complete config device add c1 devname u)" = 'unix-block,unix-char,unix-hotplug,usb' ]
    [ "$(complete config device add c1 devname disk '')" = 'boot.,ceph.,initial.,io.,limits.,path=,pool=,propagation=,raw.,readonly=,recursive=,required=,shift=,size.,size=,source.,source=' ]
    [ "$(complete config device add c1 devname gpu '')" = 'gputype=' ]
    [ "$(complete config device add c1 devname gpu gputype=)" = 'gputype=mdev,gputype=mig,gputype=physical,gputype=sriov' ]
    [ "$(complete config device add c1 devname gpu gputype=invalid '')" = '' ]
    [ "$(complete config device add c1 devname gpu gputype=mdev '')" = 'id=,mdev=,pci=,productid=,vendorid=' ]
    [ "$(complete config device add c1 devname gpu gputype=mig '')" = 'id=,mig.,pci=,productid=,vendorid=' ]
    [ "$(complete config device add c1 devname gpu gputype=physical '')" = 'gid=,id=,mode=,pci=,productid=,uid=,vendorid=' ]
    [ "$(complete config device add c1 devname gpu gputype=sriov '')" = 'id=,pci=,productid=,vendorid=' ]
    [ "$(complete config device add c1 devname infiniband '')" = 'hwaddr=,mtu=,name=,nictype=,parent=' ]
    [ "$(complete config device add c1 devname pci '')" = 'address=' ]
    [ "$(complete config device add c1 devname proxy '')" = 'bind=,connect=,gid=,listen=,mode=,nat=,proxy_protocol=,security.,uid=' ]
    [ "$(complete config device add c1 devname tpm '')" = 'path=,pathrm=' ]
    [ "$(complete config device add c1 devname unix-block '')" = 'gid=,major=,minor=,mode=,path=,required=,source=,uid=' ]
    [ "$(complete config device add c1 devname unix-char '')" = 'gid=,major=,minor=,mode=,path=,required=,source=,uid=' ]
    [ "$(complete config device add c1 devname unix-hotplug '')" = 'gid=,mode=,ownership.,productid=,required=,subsystem=,uid=,vendorid=' ]
    [ "$(complete config device add c1 devname usb '')" = 'busnum=,devnum=,gid=,mode=,productid=,required=,serial=,uid=,vendorid=' ]
    [ "$(complete config device add c1 devname nic '')" = 'network=,nictype=' ]
    [ "$(complete config device add c1 devname nic network=)" = "$(lxc query /1.0/networks?recursion=1 | jq -r '["network="+.[].name] | sort | @csv | sub("\"";"";"g")')" ]
    [ "$(complete config device add c1 devname nic network=lxdbr0 '')" = '' ]
    [ "$(complete config device add c1 devname nic nictype=)" = 'nictype=bridged,nictype=ipvlan,nictype=macvlan,nictype=ovn,nictype=p2p,nictype=physical,nictype=routed,nictype=sriov' ]
    [ "$(complete config device add c1 devname nic nictype=invalid '')" = '' ]
    [ "$(complete config device add c1 devname nic nictype=bridged '')" = 'boot.,host_name=,hwaddr=,ipv4.,ipv6.,limits.,maas.,mtu=,name=,network=,parent=,queue.,security.,vlan.,vlan=' ]
    [ "$(complete config device add c1 devname nic nictype=ipvlan '')" = 'gvrp=,hwaddr=,ipv4.,ipv6.,mode=,mtu=,name=,parent=,vlan=' ]
    [ "$(complete config device add c1 devname nic nictype=macvlan '')" = 'boot.,gvrp=,hwaddr=,maas.,mtu=,name=,network=,parent=,vlan=' ]
    [ "$(complete config device add c1 devname nic nictype=ovn '')" = 'acceleration=,boot.,host_name=,hwaddr=,ipv4.,ipv6.,name=,nested=,network=,security.,vlan=' ]
    [ "$(complete config device add c1 devname nic nictype=p2p '')" = 'boot.,host_name=,hwaddr=,ipv4.,ipv6.,limits.,mtu=,name=,queue.' ]
    [ "$(complete config device add c1 devname nic nictype=physical '')" = 'boot.,gvrp=,hwaddr=,maas.,mtu=,name=,network=,parent=,vlan=' ]
    [ "$(complete config device add c1 devname nic nictype=routed '')" = 'gvrp=,host_name=,hwaddr=,ipv4.,ipv6.,limits.,mtu=,name=,parent=,queue.,vlan=' ]
    [ "$(complete config device add c1 devname nic nictype=sriov '')" = 'boot.,hwaddr=,maas.,mtu=,name=,network=,parent=,security.,vlan=' ]
    [ "$(complete config device override '')" = 'c1,c2,localhost:' ]
    [ "$(complete config device override c)" = 'c1,c2' ]
    [ "$(complete config device override l)" = 'localhost:' ]
    [ "$(complete config device override localhost:)" = 'localhost:c1,localhost:c2' ]
    [ "$(complete config device override c1 devname '')" = 'disk,gpu,infiniband,nic,pci,proxy,tpm,unix-block,unix-char,unix-hotplug,usb' ]
    [ "$(complete config device override c1 devname u)" = 'unix-block,unix-char,unix-hotplug,usb' ]
    [ "$(complete config device override c1 devname disk '')" = 'boot.,ceph.,initial.,io.,limits.,path=,pool=,propagation=,raw.,readonly=,recursive=,required=,shift=,size.,size=,source.,source=' ]
    [ "$(complete config device override c1 devname gpu '')" = 'gputype=' ]
    [ "$(complete config device override c1 devname gpu gputype=)" = 'gputype=mdev,gputype=mig,gputype=physical,gputype=sriov' ]
    [ "$(complete config device override c1 devname gpu gputype=invalid '')" = '' ]
    [ "$(complete config device override c1 devname gpu gputype=mdev '')" = 'id=,mdev=,pci=,productid=,vendorid=' ]
    [ "$(complete config device override c1 devname gpu gputype=mig '')" = 'id=,mig.,pci=,productid=,vendorid=' ]
    [ "$(complete config device override c1 devname gpu gputype=physical '')" = 'gid=,id=,mode=,pci=,productid=,uid=,vendorid=' ]
    [ "$(complete config device override c1 devname gpu gputype=sriov '')" = 'id=,pci=,productid=,vendorid=' ]
    [ "$(complete config device override c1 devname infiniband '')" = 'hwaddr=,mtu=,name=,nictype=,parent=' ]
    [ "$(complete config device override c1 devname pci '')" = 'address=' ]
    [ "$(complete config device override c1 devname proxy '')" = 'bind=,connect=,gid=,listen=,mode=,nat=,proxy_protocol=,security.,uid=' ]
    [ "$(complete config device override c1 devname tpm '')" = 'path=,pathrm=' ]
    [ "$(complete config device override c1 devname unix-block '')" = 'gid=,major=,minor=,mode=,path=,required=,source=,uid=' ]
    [ "$(complete config device override c1 devname unix-char '')" = 'gid=,major=,minor=,mode=,path=,required=,source=,uid=' ]
    [ "$(complete config device override c1 devname unix-hotplug '')" = 'gid=,mode=,ownership.,productid=,required=,subsystem=,uid=,vendorid=' ]
    [ "$(complete config device override c1 devname usb '')" = 'busnum=,devnum=,gid=,mode=,productid=,required=,serial=,uid=,vendorid=' ]
    [ "$(complete config device override c1 devname nic '')" = 'network=,nictype=' ]
    [ "$(complete config device override c1 devname nic network=)" = "$(lxc query /1.0/networks?recursion=1 | jq -r '["network="+.[].name] | sort | @csv | sub("\"";"";"g")')" ]
    [ "$(complete config device override c1 devname nic network=lxdbr0 '')" = '' ]
    [ "$(complete config device override c1 devname nic nictype=)" = 'nictype=bridged,nictype=ipvlan,nictype=macvlan,nictype=ovn,nictype=p2p,nictype=physical,nictype=routed,nictype=sriov' ]
    [ "$(complete config device override c1 devname nic nictype=invalid '')" = '' ]
    [ "$(complete config device override c1 devname nic nictype=bridged '')" = 'boot.,host_name=,hwaddr=,ipv4.,ipv6.,limits.,maas.,mtu=,name=,network=,parent=,queue.,security.,vlan.,vlan=' ]
    [ "$(complete config device override c1 devname nic nictype=ipvlan '')" = 'gvrp=,hwaddr=,ipv4.,ipv6.,mode=,mtu=,name=,parent=,vlan=' ]
    [ "$(complete config device override c1 devname nic nictype=macvlan '')" = 'boot.,gvrp=,hwaddr=,maas.,mtu=,name=,network=,parent=,vlan=' ]
    [ "$(complete config device override c1 devname nic nictype=ovn '')" = 'acceleration=,boot.,host_name=,hwaddr=,ipv4.,ipv6.,name=,nested=,network=,security.,vlan=' ]
    [ "$(complete config device override c1 devname nic nictype=p2p '')" = 'boot.,host_name=,hwaddr=,ipv4.,ipv6.,limits.,mtu=,name=,queue.' ]
    [ "$(complete config device override c1 devname nic nictype=physical '')" = 'boot.,gvrp=,hwaddr=,maas.,mtu=,name=,network=,parent=,vlan=' ]
    [ "$(complete config device override c1 devname nic nictype=routed '')" = 'gvrp=,host_name=,hwaddr=,ipv4.,ipv6.,limits.,mtu=,name=,parent=,queue.,vlan=' ]
    [ "$(complete config device override c1 devname nic nictype=sriov '')" = 'boot.,hwaddr=,maas.,mtu=,name=,network=,parent=,security.,vlan=' ]
    [ "$(complete profile device get '')" = 'default,localhost:' ]
    [ "$(complete profile device get d)" = 'default' ]
    [ "$(complete profile device get l)" = 'localhost:' ]
    [ "$(complete profile device get localhost:)" = 'localhost:default' ]
    [ "$(complete profile device get default '')" = 'eth0,root' ]
    [ "$(complete profile device remove '')" = 'default,localhost:' ]
    [ "$(complete profile device remove d)" = 'default' ]
    [ "$(complete profile device remove l)" = 'localhost:' ]
    [ "$(complete profile device remove localhost:)" = 'localhost:default' ]
    [ "$(complete profile device remove default '')" = 'eth0,root' ]
    [ "$(complete profile device show '')" = 'default,localhost:' ]
    [ "$(complete profile device show d)" = 'default' ]
    [ "$(complete profile device show l)" = 'localhost:' ]
    [ "$(complete profile device show localhost:)" = 'localhost:default' ]
    [ "$(complete profile device show default '')" = 'eth0,root' ]
    [ "$(complete profile device unset '')" = 'default,localhost:' ]
    [ "$(complete profile device unset d)" = 'default' ]
    [ "$(complete profile device unset l)" = 'localhost:' ]
    [ "$(complete profile device unset localhost:)" = 'localhost:default' ]
    [ "$(complete profile device unset default '')" = 'eth0,root' ]

    # 'image'
    [ "$(complete image copy '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image copy l)" = 'localhost:' ]
    [ "$(complete image copy u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image copy localhost:)" = "localhost:testimage" ]
    [ "$(complete image copy localhost:testimage '')" = 'localhost:' ]
    [ "$(complete image delete '')" = "localhost:,testimage" ]
    [ "$(complete image delete l)" = 'localhost:' ]
    [ "$(complete image delete u)" = '' ]
    [ "$(complete image delete localhost:)" = "localhost:testimage" ]
    [ "$(complete image edit '')" = "localhost:,testimage" ]
    [ "$(complete image edit l)" = 'localhost:' ]
    [ "$(complete image edit u)" = '' ]
    [ "$(complete image edit localhost:)" = "localhost:testimage" ]
    [ "$(complete image export '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image export l)" = 'localhost:' ]
    [ "$(complete image export u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image export localhost:)" = "localhost:testimage" ]
    [ "$(complete image get-property '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image get-property l)" = 'localhost:' ]
    [ "$(complete image get-property u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image get-property localhost:)" = "localhost:testimage" ]
    [ "$(complete image set-property '')" = "localhost:,testimage" ]
    [ "$(complete image set-property l)" = 'localhost:' ]
    [ "$(complete image set-property u)" = '' ]
    [ "$(complete image set-property localhost:)" = "localhost:testimage" ]
    [ "$(completion_directive image import '')" = ':0' ]
    [ "$(complete image info '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image info l)" = 'localhost:' ]
    [ "$(complete image info u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image info localhost:)" = "localhost:testimage" ]
    [ "$(complete image show '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image show l)" = 'localhost:' ]
    [ "$(complete image show u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image show localhost:)" = "localhost:testimage" ]
    [ "$(complete image refresh '')" = "images:,localhost:,testimage,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image refresh l)" = 'localhost:' ]
    [ "$(complete image refresh u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image refresh localhost:)" = "localhost:testimage" ]
    [ "$(complete image refresh localhost:testimage '')" = "localhost:,testimage" ]

    # Test completions where no aliases are available.
    testimage_fingerprint="$(lxc image list -f csv -c F testimage)"
    testimage_fingerprint_prefix="$(echo "${testimage_fingerprint}" | cut -c1-12)"
    lxc image alias delete testimage
    [ "$(complete image copy '')" = "${testimage_fingerprint_prefix},images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image copy l)" = 'localhost:' ]
    [ "$(complete image copy u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image copy localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image copy localhost:testimage '')" = 'localhost:' ]
    [ "$(complete image delete '')" = "${testimage_fingerprint_prefix},localhost:" ]
    [ "$(complete image delete l)" = 'localhost:' ]
    [ "$(complete image delete u)" = '' ]
    [ "$(complete image delete localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image edit '')" = "${testimage_fingerprint_prefix},localhost:" ]
    [ "$(complete image edit l)" = 'localhost:' ]
    [ "$(complete image edit u)" = '' ]
    [ "$(complete image edit localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image export '')" = "${testimage_fingerprint_prefix},images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image export l)" = 'localhost:' ]
    [ "$(complete image export u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image export localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image get-property '')" = "${testimage_fingerprint_prefix},images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image get-property l)" = 'localhost:' ]
    [ "$(complete image get-property u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image get-property localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image set-property '')" = "${testimage_fingerprint_prefix},localhost:" ]
    [ "$(complete image set-property l)" = 'localhost:' ]
    [ "$(complete image set-property u)" = '' ]
    [ "$(complete image set-property localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(completion_directive image import '')" = ':0' ]
    [ "$(complete image info '')" = "${testimage_fingerprint_prefix},images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image info l)" = 'localhost:' ]
    [ "$(complete image info u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image info localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image show '')" = "${testimage_fingerprint_prefix},images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image show l)" = 'localhost:' ]
    [ "$(complete image show u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image show localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image refresh '')" = "${testimage_fingerprint_prefix},images:,localhost:,ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:" ]
    [ "$(complete image refresh l)" = 'localhost:' ]
    [ "$(complete image refresh u)" = 'ubuntu-daily:,ubuntu-minimal-daily:,ubuntu-minimal:,ubuntu:' ]
    [ "$(complete image refresh localhost:)" = "localhost:${testimage_fingerprint_prefix}" ]
    [ "$(complete image refresh localhost:testimage '')" = "${testimage_fingerprint_prefix},localhost:" ]

    # 'image alias'
    lxc image alias create testimage "${testimage_fingerprint}"
    [ "$(complete image alias create '')" = 'localhost:' ]
    [ "$(complete image alias create foo '')" = "${testimage_fingerprint}" ]
    [ "$(complete image alias delete '')" = 'localhost:,testimage' ]
    [ "$(complete image alias rename '')" = 'localhost:,testimage' ]
    [ "$(complete image alias delete '')" = 'localhost:,testimage' ]
    [ "$(complete image alias rename '')" = 'localhost:,testimage' ]

    lxc delete c1 c2 -f
}
