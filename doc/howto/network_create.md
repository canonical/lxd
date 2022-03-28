The desired type can be specified using the `--type` argument, e.g.

```bash
lxc network create <name> --type=bridge [options...]
```

If no `--type` argument is specified, the default type of `bridge` is used.


The configuration keys are namespaced with the following namespaces currently supported for all network types:

 - `maas` (MAAS network identification)
 - `user` (free form key/value for user metadata)
