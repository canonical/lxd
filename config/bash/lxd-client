_have lxc && {
  _lxd_complete()
  {
    _lxd_names()
    {
      local state=$1
      local keys=$2

      local cmd="lxc list --fast"
      [ -n "$state" ] && cmd="$cmd | grep -E '$state'"

      COMPREPLY=( $( compgen -W \
        "$( eval $cmd | grep -Ev '(\+--|NAME)' | awk '{print $2}' ) $keys" "$cur" )
      )
    }

    _lxd_images()
    {
      COMPREPLY=( $( compgen -W \
        "$( lxc image list | tail -n +4 | awk '{print $2}' | egrep -v '^(\||^$)' )" "$cur" )
      )
    }

    _lxd_remotes()
    {
      COMPREPLY=( $( compgen -W \
        "$( lxc remote list | tail -n +4 | awk '{print $2}' | egrep -v '^(\||^$)' )" "$cur" )
      )
    }

    _lxd_profiles()
    {
      COMPREPLY=( $( compgen -W "$( lxc profile list | tail -n +4 | awk '{print $2}' | egrep -v '^(\||^$)' )" "$cur" ) )
    }

    _lxd_networks()
    {
      COMPREPLY=( $( compgen -W \
        "$( lxc network list | tail -n +4 | awk '{print $2}' | egrep -v '^(\||^$)' )" "$cur" )
      )
    }

    _lxd_storage_pools()
    {
      COMPREPLY=( $( compgen -W \
        "$( lxc storage list | tail -n +4 | awk '{print $2}' | egrep -v '^(\||^$)' )" "$cur" )
      )
    }

    _lxd_storage_volumes()
    {
      COMPREPLY=( $( compgen -W \
        "$( lxc storage volume list | tail -n +4 | awk '{print $2}' | egrep -v '^(\||^$)' )" "$cur" )
      )
    }

    COMPREPLY=()
    # ignore special --foo args
    if [[ ${COMP_WORDS[COMP_CWORD]} == -* ]]; then
      return 0
    fi

    lxc_cmds="config copy delete exec file finger help image info init launch \
      list manpage monitor move network profile publish query remote rename \
      restart restore shell snapshot start stop storage version"

    global_keys="core.https_address core.https_allowed_credentials \
      core.https_allowed_headers core.https_allowed_methods \
      core.https_allowed_origin core.macaroon.endpoint core.proxy_https \
      core.proxy_http core.proxy_ignore_hosts core.trust_password \
      images.auto_update_cached images.auto_update_interval \
      images.compression_algorithm images.remote_cache_expiry"

    container_keys="boot.autostart boot.autostart.delay \
      boot.autostart.priority boot.host_shutdown_timeout environment. \
      limits.cpu limits.cpu.allowance limits.cpu.priority \
      limits.disk.priority limits.memory limits.memory.enforce \
      limits.memory.swap limits.memory.swap.priority limits.network.priority \
      limits.processes linux.kernel_modules migration.incremental.memory \
      migration.incremental.memory.goal \
      migration.incremental.memory.iterations raw.apparmor raw.idmap raw.lxc \
      raw.seccomp security.idmap.base security.idmap.isolated \
      security.idmap.size security.devlxd security.nesting security.privileged \
      security.syscalls.blacklist security.syscalls.blacklist_compat \
      security.syscalls.blacklist_default \
      volatile.apply_quota volatile.apply_template volatile.base_image \
      volatile.idmap.base volatile.idmap.next volatile.last_state.idmap \
      volatile.last_state.power user.meta-data user.network-config \
      user.network_mode user.user-data user.vendor-data"

    networks_keys="bridge.driver bridge.external_interfaces bridge.mode \
      bridge.mtu dns.domain dns.mode fan.overlay_subnet fan.type \
      fan.underlay_subnet ipv4.address ipv4.dhcp ipv4.dhcp.expiry \
      ipv4.dhcp.ranges ipv4.firewall ipv4.nat ipv4.routes ipv4.routing \
      ipv6.address ipv6.dhcp ipv6.dhcp.expiry ipv6.dhcp.ranges \
      ipv6.dhcp.stateful ipv6.firewall ipv6.nat ipv6.routes ipv6.routing \
      raw.dnsmasq"

    storage_pool_keys="source size btrfs.mount_options ceph.cluster_name \
      ceph.osd.force_reuse ceph.osd.pg_num ceph.osd.pool_name \
      ceph.rbd.clone_copy ceph.user.name lvm.thinpool_name lvm.use_thinpool \
      lvm.vg_name rsync.bwlimit volatile.initial_source \
      volatile.pool.pristine volume.block.filesystem \
      volume.block.mount_options volume.size volume.zfs.remove_snapshots \
      volume.zfs.use_refquota zfs.clone_copy zfs.pool_name"

    storage_volume_keys="size block.filesystem block.mount_options \
      zfs.remove_snapshots zfs.use_refquota"

    if [ $COMP_CWORD -eq 1 ]; then
      COMPREPLY=( $(compgen -W "$lxc_cmds" -- ${COMP_WORDS[COMP_CWORD]}) )
      return 0
    fi

    local no_dashargs
    cur=${COMP_WORDS[COMP_CWORD]}

    no_dashargs=(${COMP_WORDS[@]// -*})
    pos=$((COMP_CWORD - (${#COMP_WORDS[@]} - ${#no_dashargs[@]})))
    if [ -z "$cur" ]; then
      pos=$(($pos + 1))
    fi

    case ${no_dashargs[1]} in
      "config")
        case $pos in
          2)
            COMPREPLY=( $(compgen -W "get set unset show edit metadata template device trust" -- $cur) )
            ;;
          3)
            case ${no_dashargs[2]} in
              "trust")
                COMPREPLY=( $(compgen -W "list add remove" -- $cur) )
                ;;
              "device")
                COMPREPLY=( $(compgen -W "add get set unset list show remove" -- $cur) )
                ;;
              "metadata")
                COMPREPLY=( $(compgen -W "show edit" -- $cur) )
                ;;
              "template")
                COMPREPLY=( $(compgen -W "list show create edit delete" -- $cur) )
                ;;
              "show"|"edit")
                _lxd_names
                ;;
              "get"|"set"|"unset")
                _lxd_names "" "$global_keys"
                ;;
            esac
            ;;
          4)
            case ${no_dashargs[2]} in
              "trust")
                _lxd_remotes
                ;;
              "device")
                _lxd_names
                ;;
              "get"|"set"|"unset")
                COMPREPLY=( $(compgen -W "$container_keys" -- $cur) )
                ;;
            esac
            ;;
        esac
        ;;
      "copy")
        if [ $pos -lt 4 ]; then
          _lxd_names
        fi
        ;;
      "delete")
        _lxd_names
        ;;
      "exec")
        _lxd_names "RUNNING"
        ;;
      "file")
        COMPREPLY=( $(compgen -W "pull push edit delete" -- $cur) )
        ;;
      "help")
        COMPREPLY=( $(compgen -W "$lxc_cmds" -- $cur) )
        ;;
      "image")
        COMPREPLY=( $(compgen -W "import copy delete refresh export info list show edit alias" -- $cur) )
        ;;
      "info")
        _lxd_names
        ;;
      "init")
        _lxd_images
        ;;
      "launch")
        _lxd_images
        ;;
      "move")
        _lxd_names
        ;;
      "network")
        case $pos in
          2)
            COMPREPLY=( $(compgen -W "list show create get set unset delete edit rename attach attach-profile detach detach-profile" -- $cur) )
            ;;
          3)
            case ${no_dashargs[2]} in
              "show"|"get"|"set"|"unset"|"delete"|"edit"|"rename"|"attach"|"attach-profile"|"detach"|"detach-profile")
                _lxd_networks
                ;;
            esac
            ;;
          4)
            case ${no_dashargs[2]} in
              "get"|"set"|"unset")
                COMPREPLY=( $(compgen -W "$networks_keys" -- $cur) )
                ;;
              "attach"|"detach"|"detach-profile")
                _lxd_names
                ;;
              "attach-profile")
                _lxd_profiles
                ;;
            esac
        esac
        ;;
      "profile")
        case $pos in
          2)
            COMPREPLY=( $(compgen -W "list show create copy get set unset delete edit rename assign add remove device " -- $cur) )
            ;;
          3)
            case ${no_dashargs[2]} in
              "device")
                COMPREPLY=( $(compgen -W "list show remove get set unset add" -- $cur) )
                ;;
              "add"|"assign"|"remove")
                _lxd_names
                ;;
              *)
                _lxd_profiles
                ;;
            esac
            ;;
          4)
            case ${no_dashargs[2]} in
              "device"|"add"|"assign"|"remove")
                _lxd_profiles
                ;;
              *)
                COMPREPLY=( $(compgen -W "$container_keys" -- $cur) )
                ;;
            esac
            ;;
        esac
        ;;
      "publish")
        _lxd_names
        ;;
      "remote")
        COMPREPLY=( $(compgen -W \
          "add remove list rename set-url set-default get-default" -- $cur) )
        ;;
      "restart")
        _lxd_names
        ;;
      "restore")
        _lxd_names
        ;;
      "shell")
        _lxd_names "RUNNING"
        ;;
      "snapshot")
        _lxd_names
        ;;
      "start")
        _lxd_names "STOPPED|FROZEN"
        ;;
      "stop")
        _lxd_names "RUNNING"
        ;;
      "storage_pools")
        case $pos in
          2)
            COMPREPLY=( $(compgen -W "list show create get set unset delete edit" -- $cur) )
            ;;
          3)
            case ${no_dashargs[2]} in
              "show"|"get"|"set"|"unset"|"delete"|"edit")
                _lxd_storage_pools
                ;;
            esac
            ;;
          4)
            case ${no_dashargs[2]} in
              "get"|"set"|"unset")
                COMPREPLY=( $(compgen -W "$storage_pool_keys" -- $cur) )
                ;;
            esac
        esac
        ;;
      "storage_volumes")
        case $pos in
          2)
            COMPREPLY=( $(compgen -W "list show create rename get set unset delete edit attach attach-profile detach detach-profile" -- $cur) )
            ;;
          3)
            case ${no_dashargs[2]} in
              "show"|"get"|"set"|"unset"|"rename"|"delete"|"edit"|"attach"|"attach-profile"|"detach"|"detach-profile")
                _lxd_storage_volumes
                ;;
            esac
            ;;
          4)
            case ${no_dashargs[2]} in
              "get"|"set"|"unset")
                COMPREPLY=( $(compgen -W "$storage_volume_keys" -- $cur) )
                ;;
              "attach"|"detach"|"detach-profile")
                _lxd_names
                ;;
              "attach-profile")
                _lxd_profiles
                ;;
            esac
        esac
        ;;
      *)
        ;;
    esac

    return 0
  }

  complete -o default -F _lxd_complete lxc
}
