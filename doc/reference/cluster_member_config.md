(cluster-member-config)=
# Cluster member configuration

Each cluster member has its own key/value configuration with the following supported namespaces:

- `user` (free form key/value for user metadata)
- `scheduler` (options related to how the member is automatically targeted by the cluster)

The following keys are currently supported:

| Key                   | Type      | Default | Description |
| :-------------------- | :-------- | :------ | :---------- |
| `scheduler.instance`  | string    | `all`   | Possible values are `all`, `manual` and `group`. See {ref}`clustering-assignment` for more information.|
| `user.*`              | string    | -       | Free form user key/value storage (can be used in search). |
