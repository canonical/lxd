# Introduction
The LXD client and daemon respect some environment variables to adapt to
the user's environment and to turn some advanced features on and off.

# Common
LXD\_DIR                        | The LXD data directory
PATH                            | List of paths to look into when resolving binaries

# Client environment variable
Name                            | Description
:---                            | :----
EDITOR                          | What text editor to use
VISUAL                          | What text editor to use (if EDITOR isn't set)

# Server environment variable
Name                            | Description
:---                            | :----
LXD\_SECURITY\_APPARMOR         | If set to "false", forces AppArmor off
LXD\_LXC\_TEMPLATE\_CONFIG      | Path to the LXC template configuration directory
