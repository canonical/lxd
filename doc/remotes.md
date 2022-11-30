# Remotes

## Introduction

Remotes are a concept in the LXD command line client which are used to refer to various LXD servers or clusters.
A remote is effectively a name pointing to the URL of a particular LXD server as well as needed credentials to login and authenticate the server.
LXD has four types of remotes:

- Static
- Default
- Global (per-system)
- Local (per-user)

### Static

Static remotes are:

- `local` (default)
- `ubuntu`
- `ubuntu-daily`

They are hardcoded and can't be modified by the user.

### Default

Automatically added on first use.

### Global (per-system)

By default the global configuration file is kept in either `/etc/lxd/config.yml`, or `/var/snap/lxd/common/global-conf/` for the snap version, or in `LXD_GLOBAL_CONF` if defined.
The configuration file can be manually edited to add global remotes. Certificates for those remotes should be stored inside the `servercerts` directory (e.g. `/etc/lxd/servercerts/`) and match the remote name (e.g. `foo.crt`).

An example configuration is below:

```
remotes:
  foo:
    addr: https://10.0.2.4:8443
    auth_type: tls
    project: default
    protocol: lxd
    public: false
  bar:
    addr: https://10.0.2.5:8443
    auth_type: tls
    project: default
    protocol: lxd
    public: false
```

### Local (per-user)

Local level remotes are managed from the CLI (`lxc`) with:
`lxc remote [command]`

By default the configuration file is kept in `~/.config/lxc/config.yml`, or `~/snap/lxd/common/config/config.yml` for the snap version, or in `LXD_CONF` if defined.
Users have the possibility to override system remotes (e.g. by running `lxc remote rename` or `lxc remote set-url`)
which results in the remote being copied to their own configuration, including any associated certificates.
