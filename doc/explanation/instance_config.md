(instance-config)=
# Instance configuration

The instance configuration consists of different categories:

Instance properties
: Instance properties are set when the instance is created.
  They include, for example, the instance name and architecture.
  These properties are specified during instance creation and cannot be changed in the same way as other configuration options.

  In the YAML configuration, properties are on the top level.

  See {ref}`instance-properties` for a reference of available instance properties.

Instance options
: Instance options are configuration options that are related directly to the instance.
  They include, for example, startup options, security settings, hardware limits, kernel modules, snapshots and user keys.
  These options can be specified as key/value pairs during instance creation (through the `--config key=value` flag).
  After creation, they can be configured with the `lxc config set` and `lxc config unset` commands.

  In the YAML configuration, options are located under the `config` entry.

  See {ref}`instance-options` for a reference of available instance options.

```{toctree}
:maxdepth: 1
:hidden:

../reference/instance_properties.md
../reference/instance_options.md
../reference/instance_units.md
```
