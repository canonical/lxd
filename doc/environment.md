# Environment variables

## Introduction
The LXD client and daemon respect some environment variables to adapt to
the user's environment and to turn some advanced features on and off.

## Common
Name                            | Description
:---                            | :----
`LXD_DIR`                       | The LXD data directory
`LXD_INSECURE_TLS`              | If set to true, allows all default Go ciphers both for client <-> server communication and server <-> image servers (server <-> server and clustering are not affected)
`PATH`                          | List of paths to look into when resolving binaries
`http_proxy`                    | Proxy server URL for HTTP
`https_proxy`                   | Proxy server URL for HTTPs
`no_proxy`                      | List of domains that don't require the use of a proxy

## Client environment variable
Name                            | Description
:---                            | :----
`EDITOR`                        | What text editor to use
`VISUAL`                        | What text editor to use (if `EDITOR` isn't set)

## Server environment variable
Name                            | Description
:---                            | :----
`LXD_EXEC_PATH`                 | Full path to the LXD binary (used when forking subcommands)
`LXD_LXC_TEMPLATE_CONFIG`       | Path to the LXC template configuration directory
`LXD_SECURITY_APPARMOR`         | If set to `false`, forces AppArmor off
`LXD_UNPRIVILEGED_ONLY`         | If set to `true`, enforces that only unprivileged containers can be created. Note that any privileged containers that have been created before setting LXD_UNPRIVILEGED_ONLY will continue to be privileged. To use this option effectively it should be set when the LXD daemon is first setup.
