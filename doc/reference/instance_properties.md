(instance-properties)=
# Instance properties

The following are direct instance properties.
They can't be part of a {ref}`profile <profiles>`:

- `name`
- `architecture`

Name is the instance name and can only be changed by renaming the instance.

Valid instance names must:

- Be between 1 and 63 characters long
- Be made up exclusively of letters, numbers and dashes from the ASCII table
- Not start with a digit or a dash
- Not end with a dash

This requirement is so that the instance name may properly be used in
DNS records, on the file system, in various security profiles as well as
the host name of the instance itself.
