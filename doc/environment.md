# Environment variables
## Introduction
The LXD client and daemon respect some environment variables to adapt to
the user's environment and to turn some advanced features on and off.

## Common
Name                            | Description
:---                            | :----
`LXD_DIR`                       | The LXD data directory
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
