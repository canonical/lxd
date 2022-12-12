(instances-snapshots)=
# How to create instance snapshots

You can save your instance at a point in time by creating an instance snapshot, which makes it easy to restore the instance to a previous state.

Instance snapshots are stored in the same storage pool as the instance volume itself.

## Create a snapshot

Use the following command to create a snapshot of an instance:

    lxc snapshot <instance_name> [<snapshot name>]

% Include content from [storage_backup_volume.md](storage_backup_volume.md)
```{include} storage_backup_volume.md
    :start-after: <!-- Include start create snapshot options -->
    :end-before: <!-- Include end create snapshot options -->
```

For virtual machines, you can add the `--stateful` flag to capture not only the data included in the instance volume but also the running state of the instance.
Note that this feature is not fully supported for containers because of CRIU limitations.

## View, edit or delete snapshots

Use the following command to display the snapshots for an instance:

    lxc info <instance_name>

You can view or modify snapshots in a similar way to instances, by referring to the snapshot with `<instance_name>/<snapshot_name>`.

To show configuration information about a snapshot, use the following command:

    lxc config show <instance_name>/<snapshot_name>

To change the expiry date of a snapshot, use the following command:

    lxc config edit <instance_name>/<snapshot_name>

```{note}
In general, snapshots cannot be edited, because they preserve the state of the instance.
The only exception is the expiry date.
Other changes to the configuration are silently ignored.
```

To delete a snapshot, use the following command:

    lxc delete <instance_name>/<snapshot_name>

## Schedule instance snapshots

You can configure an instance to automatically create snapshots at specific times (at most once every minute).
To do so, set the [`snapshots.schedule`](instance-options-snapshots) instance option.

For example, to configure daily snapshots, use the following command:

    lxc config set <instance_name> snapshots.schedule @daily

To configure taking a snapshot every day at 6 am, use the following command:

    lxc config set <instance_name> snapshots.schedule "0 6 * * *"

When scheduling regular snapshots, consider setting an automatic expiry ([`snapshots.expiry`](instance-options-snapshots)) and a naming pattern for snapshots ([`snapshots.pattern`](instance-options-snapshots)).
You should also configure whether you want to take snapshots of instances that are not running ([`snapshots.schedule.stopped`](instance-options-snapshots)).

## Restore an instance snapshot

You can restore an instance to any of its snapshots.

To do so, use the following command:

    lxc restore <instance_name> <snapshot_name>

If the snapshot is stateful (which means that it contains information about the running state of the instance), you can add the `--stateful` flag to restore the state.
