(cluster-member-config)=
# Cluster member configuration

Each cluster member has its own key/value configuration with the following supported namespaces:

- `user` (free form key/value for user metadata)
- `scheduler` (options related to how the member is automatically targeted by the cluster)

The currently supported keys are:

| Key                   | Type      | Default | Description |
| :-------------------- | :-------- | :------ | :---------- |
| `scheduler.instance`  | string    | `all`   | If `all` then the member will be auto-targeted for instance creation if it has the least number of instances. If `manual` then instances will only target the member if `--target` is given. If `group` then instances will only target members in the group provided using `--target=@<group>` |
| `user.*`             | string    | -       | Free form user key/value storage (can be used in search) |
