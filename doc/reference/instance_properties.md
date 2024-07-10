(instance-properties)=
# Instance properties

Instance properties are set when the instance is created.
They cannot be part of a {ref}`profile <profiles>`.

The following instance properties are available:

% Include content from [../metadata.txt](../metadata.txt)
```{include} ../metadata.txt
    :start-after: <!-- config group instance-property-instance-conf start -->
    :end-before: <!-- config group instance-property-instance-conf end -->
```

(instance-name-requirements)=
## Instance name requirements

The instance name can be changed only by renaming the instance with the [`lxc rename`](lxc_rename.md) command.

Valid instance names must fulfill the following requirements:

- The name must be between 1 and 63 characters long.
- The name must contain only letters, numbers and dashes from the ASCII table.
- The name must not start with a digit or a dash.
- The name must not end with a dash.

The purpose of these requirements is to ensure that the instance name can be used in DNS records, on the file system, in various security profiles and as the host name of the instance itself.
