---
relatedlinks: "[How&#32;to&#32;install&#32;a&#32;Windows&#32;11&#32;VM&#32;using&#32;LXD](https://ubuntu.com/tutorials/how-to-install-a-windows-11-vm-using-lxd)"
myst:
  html_meta:
    description: An index of how-to guides for LXD instances, including creating, configuring, and managing instances, backup, migration, and GPU passthrough.
---

(instances)=
# Instances

These how-to guides cover common operations related to LXD instances.

## Create and manage instances

LXD supports both system containers and virtual machines, configured using direct settings or reusable profiles.

```{toctree}
:titlesonly:

Create instances </howto/instances_create.md>
Configure instances </howto/instances_configure.md>
Manage instances </howto/instances_manage.md>
Use profiles </profiles.md>
Troubleshoot errors </howto/instances_troubleshoot.md>
```

## Attach instances to Ubuntu Pro

An LXD server can automatically attach guest instances to its Ubuntu Pro subscription.

```{toctree}
:titlesonly:

Auto attach Ubuntu Pro </howto/instances_ubuntu_pro_attach.md>
```

## Work with instances

Instance files can be accessed from the host, and the instance console can be attached to for log output and debugging. Commands can also be run inside instances through the `lxc` CLI or by opening a shell.

```{toctree}
:titlesonly:

Access files </howto/instances_access_files.md>
Access the console </howto/instances_console.md>
Run commands </instance-exec.md>
Use cloud-init </cloud-init>
Add a routed NIC to a VM </howto/instances_routed_nic_vm.md>
```

## Back up, import, and migrate instances

Instances can be backed up using snapshots, export files, or copies. Physical machines, as well as virtual machines and containers created using a different technology, can be imported as LXD instances. Instances can also be migrated between LXD servers, including live migration for VMs.

```{toctree}
:titlesonly:

Back up instances </howto/instances_backup.md>
Import existing machines </howto/import_machines_to_instances>
Migrate instances </howto/instances_migrate>
```

## Pass through an NVIDIA GPU

An NVIDIA GPU can be passed through to a container running a Docker workload.

```{toctree}
:titlesonly:

Pass NVIDIA GPUs </howto/container_gpu_passthrough_with_docker>
```

## Related topics

{{instances_exp}}

{{instances_ref}}
