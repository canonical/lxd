---
discourse: 15871
---

(cluster-form)=
# How to form a cluster

When forming a LXD cluster, you start with a bootstrap server.
This bootstrap server can be an existing LXD server or a newly installed one.

After initializing the bootstrap server, you can join additional servers to the cluster.
See {ref}`clustering-members` for more information.

You can form the LXD cluster interactively by providing configuration information during the initialization process or by using preseed files that contain the full configuration.

To quickly and automatically set up a basic LXD cluster, you can use MicroCloud.
Note, however, that this project is still in an early phase.

## Configure the cluster interactively

To form your cluster, you must first run `lxd init` on the bootstrap server. After that, run it on the other servers that you want to join to the cluster.

When forming a cluster interactively, you answer the questions that `lxd init` prompts you with to configure the cluster.

### Initialize the bootstrap server

To initialize the bootstrap server, run `lxd init` and answer the questions according to your desired configuration.

You can accept the default values for most questions, but make sure to answer the following questions accordingly:

- `Would you like to use LXD clustering?`

  Select **yes**.
- `What IP address or DNS name should be used to reach this server?`

  Make sure to use an IP or DNS address that other servers can reach.
- `Are you joining an existing cluster?`

  Select **no**.
- `Setup password authentication on the cluster?`

  Select **no** to use {ref}`authentication tokens <authentication-token>` (recommended) or **yes** to use a {ref}`trust password <authentication-trust-pw>`.

<details>
<summary>Expand to see a full example for <code>lxd init</code> on the bootstrap server</summary>

```{terminal}
:input: lxd init

Would you like to use LXD clustering? (yes/no) [default=no]: yes
What IP address or DNS name should be used to reach this server? [default=192.0.2.101]:
Are you joining an existing cluster? (yes/no) [default=no]: no
What member name should be used to identify this server in the cluster? [default=server1]:
Setup password authentication on the cluster? (yes/no) [default=no]: no
Do you want to configure a new local storage pool? (yes/no) [default=yes]:
Name of the storage backend to use (btrfs, dir, lvm, zfs) [default=zfs]:
Create a new ZFS pool? (yes/no) [default=yes]:
Would you like to use an existing empty block device (e.g. a disk or partition)? (yes/no) [default=no]:
Size in GiB of the new loop device (1GiB minimum) [default=9GiB]:
Do you want to configure a new remote storage pool? (yes/no) [default=no]:
Would you like to connect to a MAAS server? (yes/no) [default=no]:
Would you like to configure LXD to use an existing bridge or host interface? (yes/no) [default=no]:
Would you like to create a new Fan overlay network? (yes/no) [default=yes]:
What subnet should be used as the Fan underlay? [default=auto]:
Would you like stale cached images to be updated automatically? (yes/no) [default=yes]:
Would you like a YAML "lxd init" preseed to be printed? (yes/no) [default=no]:
```

</details>

After the initialization process finishes, your first cluster member should be up and available on your network.
You can check this with `lxc cluster list`.

### Join additional servers

You can now join further servers to the cluster.

```{note}
The servers that you add should be newly installed LXD servers.
If you are using existing servers, make sure to clear their contents before joining them, because any existing data on them will be lost.
```

To join a server to the cluster, run `lxd init` on the cluster.
Joining an existing cluster requires root privileges, so make sure to run the command as root or with `sudo`.

Basically, the initialization process consists of the following steps:

1. Request to join an existing cluster.

   Answer the first questions that `lxd init` asks accordingly:

   - `Would you like to use LXD clustering?`

     Select **yes**.
   - `What IP address or DNS name should be used to reach this server?`

     Make sure to use an IP or DNS address that other servers can reach.
   - `Are you joining an existing cluster?`

     Select **yes**.
   - `Do you have a join token?`

     Select **yes** if you configured the bootstrap server to use {ref}`authentication tokens <authentication-token>` (recommended) or **no** if you configured it to use a {ref}`trust password <authentication-trust-pw>`.
1. Authenticate with the cluster.

   There are two alternative methods, depending on which authentication method you choose when configuring the bootstrap server.

   `````{tabs}

   ````{group-tab} Authentication tokens (recommended)
   If you configured your cluster to use {ref}`authentication tokens <authentication-token>`, you must generate a join token for each new member.
   To do so, run the following command on an existing cluster member (for example, the bootstrap server):

       lxc cluster add <new_member_name>

   This command returns a single-use join token that is valid for a configurable time (see [`cluster.join_token_expiry`](server-options-cluster)).
   Enter this token when `lxd init` prompts you for the join token.

   The join token contains the addresses of the existing online members, as well as a single-use secret and the fingerprint of the cluster certificate.
   This reduces the amount of questions that you must answer during `lxd init`, because the join token can be used to answer these questions automatically.
   ````
   ````{group-tab} Trust password
   If you configured your cluster to use a {ref}`trust password <authentication-trust-pw>`, `lxd init` requires more information about the cluster before it can start the authorization process:

   1. Specify a name for the new cluster member.
   1. Provide the address of an existing cluster member (the bootstrap server or any other server you have already added).
   1. Verify the fingerprint for the cluster.
   1. If the fingerprint is correct, enter the trust password to authorize with the cluster.
   ````

   `````

1. Confirm that all local data for the server is lost when joining a cluster.
1. Configure server-specific settings (see {ref}`clustering-member-config` for more information).

   You can accept the default values or specify custom values for each server.

<details>
<summary>Expand to see full examples for <code>lxd init</code> on additional servers</summary>

`````{tabs}

````{group-tab} Authentication tokens (recommended)

```{terminal}
:input: sudo lxd init

Would you like to use LXD clustering? (yes/no) [default=no]: yes
What IP address or DNS name should be used to reach this server? [default=192.0.2.102]:
Are you joining an existing cluster? (yes/no) [default=no]: yes
Do you have a join token? (yes/no/[token]) [default=no]: yes
Please provide join token: eyJzZXJ2ZXJfbmFtZSI6InJwaTAxIiwiZmluZ2VycHJpbnQiOiIyNjZjZmExZDk0ZDZiMjk2Nzk0YjU0YzJlYzdjOTMwNDA5ZjIzNjdmNmM1YjRhZWVjOGM0YjAxYTc2NjU0MjgxIiwiYWRkcmVzc2VzIjpbIjE3Mi4xNy4zMC4xODM6ODQ0MyJdLCJzZWNyZXQiOiJmZGI1OTgyNjgxNTQ2ZGQyNGE2ZGE0Mzg5MTUyOGM1ZGUxNWNmYmQ5M2M3OTU3ODNkNGI5OGU4MTQ4MWMzNmUwIn0=
All existing data is lost when joining a cluster, continue? (yes/no) [default=no] yes
Choose "size" property for storage pool "local":
Choose "source" property for storage pool "local":
Choose "zfs.pool_name" property for storage pool "local":
Would you like a YAML "lxd init" preseed to be printed? (yes/no) [default=no]:
```

````
````{group-tab} Trust password

```{terminal}
:input: sudo lxd init

Would you like to use LXD clustering? (yes/no) [default=no]: yes
What IP address or DNS name should be used to reach this server? [default=192.0.2.102]:
Are you joining an existing cluster? (yes/no) [default=no]: yes
Do you have a join token? (yes/no/[token]) [default=no]: no
What member name should be used to identify this server in the cluster? [default=server2]:
IP address or FQDN of an existing cluster member (may include port): 192.0.2.101:8443
Cluster fingerprint: 2915dafdf5c159681a9086f732644fb70680533b0fb9005b8c6e9bca51533113
You can validate this fingerprint by running "lxc info" locally on an existing cluster member.
Is this the correct fingerprint? (yes/no/[fingerprint]) [default=no]: yes
Cluster trust password:
All existing data is lost when joining a cluster, continue? (yes/no) [default=no] yes
Choose "size" property for storage pool "local":
Choose "source" property for storage pool "local":
Choose "zfs.pool_name" property for storage pool "local":
Would you like a YAML "lxd init" preseed to be printed? (yes/no) [default=no]:
```

````
`````

</details>

After the initialization process finishes, your server is added as a new cluster member.
You can check this with `lxc cluster list`.

## Configure the cluster through preseed files

To form your cluster, you must first run `lxd init` on the bootstrap server.
After that, run it on the other servers that you want to join to the cluster.

Instead of answering the `lxd init` questions interactively, you can provide the required information through preseed files.
You can feed a file to `lxd init` with the following command:

    cat <preseed-file> | lxd init --preseed

You need a different preseed file for every server.

### Initialize the bootstrap server

The required contents of the preseed file depend on whether you want to use {ref}`authentication tokens <authentication-token>` (recommended) or a {ref}`trust password <authentication-trust-pw>` for authentication.

`````{tabs}

````{group-tab} Authentication tokens (recommended)
To enable clustering, the preseed file for the bootstrap server must contain the following fields:

```yaml
config:
  core.https_address: <IP_address_and_port>
cluster:
  server_name: <server_name>
  enabled: true
```

Here is an example preseed file for the bootstrap server:

```yaml
config:
  core.https_address: 192.0.2.101:8443
  images.auto_update_interval: 15
storage_pools:
- name: default
  driver: dir
networks:
- name: lxdbr0
  type: bridge
profiles:
- name: default
  devices:
    root:
      path: /
      pool: default
      type: disk
    eth0:
      name: eth0
      nictype: bridged
      parent: lxdbr0
      type: nic
cluster:
  server_name: server1
  enabled: true
```

````
````{group-tab} Trust password
To enable clustering, the preseed file for the bootstrap server must contain the following fields:

```yaml
config:
  core.https_address: <IP_address_and_port>
  core.trust_password: <trust_password>
cluster:
  server_name: <server_name>
  enabled: true
```

Here is an example preseed file for the bootstrap server:

```yaml
config:
  core.trust_password: the_password
  core.https_address: 192.0.2.101:8443
  images.auto_update_interval: 15
storage_pools:
- name: default
  driver: dir
networks:
- name: lxdbr0
  type: bridge
profiles:
- name: default
  devices:
    root:
      path: /
      pool: default
      type: disk
    eth0:
      name: eth0
      nictype: bridged
      parent: lxdbr0
      type: nic
cluster:
  server_name: server1
  enabled: true
```

````
`````

### Join additional servers

The required contents of the preseed files depend on whether you configured the bootstrap server to use {ref}`authentication tokens <authentication-token>` (recommended) or a {ref}`trust password <authentication-trust-pw>` for authentication.

The preseed files for new cluster members require only a `cluster` section with data and configuration values that are specific to the joining server.

`````{tabs}

````{group-tab} Authentication tokens (recommended)
The preseed file for additional servers must include the following fields:

```yaml
cluster:
  enabled: true
  server_address: <IP_address_of_server>
  cluster_token: <join_token>
```

Here is an example preseed file for a new cluster member:

```yaml
cluster:
  enabled: true
  server_address: 192.0.2.102:8443
  cluster_token: eyJzZXJ2ZXJfbmFtZSI6Im5vZGUyIiwiZmluZ2VycHJpbnQiOiJjZjlmNmVhMWIzYjhiNjgxNzQ1YTY1NTY2YjM3ZGUwOTUzNjRmM2MxMDAwMGNjZWQyOTk5NDU5YzY2MGIxNWQ4IiwiYWRkcmVzc2VzIjpbIjE3Mi4xNy4zMC4xODM6ODQ0MyJdLCJzZWNyZXQiOiIxNGJmY2EzMDhkOTNhY2E3MGJmYThkMzE0NWM4NWY3YmE0ZmU1YmYyNmJiNDhmMmUwNzhhOGZhMDczZDc0YTFiIn0=
  member_config:
  - entity: storage-pool
    name: default
    key: source
    value: ""
```

````
````{group-tab} Trust password
The preseed file for additional servers must include the following fields:

```yaml
cluster:
  server_name: <server_name>
  enabled: true
  cluster_address: <IP_address_of_bootstrap_server>
  server_address: <IP_address_of_server>
  cluster_password: <trust_password>
  cluster_certificate: <certificate> # use this or cluster_certificate_path
  cluster_certificate_path: <path_to-certificate_file> # use this or cluster_certificate
```

  To create a YAML-compatible entry for the `cluster_certificate` key, run one the following commands on the bootstrap server:

   - When using the snap: `sed ':a;N;$!ba;s/\n/\n\n/g' /var/snap/lxd/common/lxd/cluster.crt`
   - Otherwise: `sed ':a;N;$!ba;s/\n/\n\n/g' /var/lib/lxd/cluster.crt`

  Alternatively, copy the `cluster.crt` file from the bootstrap server to the server that you want to join and specify its path in the `cluster_certificate_path` key.

Here is an example preseed file for a new cluster member:

```yaml
cluster:
  server_name: server2
  enabled: true
  server_address: 192.0.2.102:8443
  cluster_address: 192.0.2.101:8443
  cluster_certificate: "-----BEGIN CERTIFICATE-----

opyQ1VRpAg2sV2C4W8irbNqeUsTeZZxhLqp4vNOXXBBrSqUCdPu1JXADV0kavg1l

2sXYoMobyV3K+RaJgsr1OiHjacGiGCQT3YyNGGY/n5zgT/8xI0Dquvja0bNkaf6f

...

-----END CERTIFICATE-----
"
  cluster_password: the_password
  member_config:
  - entity: storage-pool
    name: default
    key: source
    value: ""
```

````
`````

## Use MicroCloud

```{youtube} https://www.youtube.com/watch?v=ZSZoLnp-Ip0
```

Instead of setting up your LXD cluster manually, you can use [MicroCloud](https://snapcraft.io/microcloud) to get a fully highly available LXD cluster with Ceph storage up and running.

To install the required snaps, run the following command:

    snap install lxd microceph microcloud

Then start the bootstrapping process with the following command:

    microcloud init

During the initialization process, MicroCloud detects the other servers, sets up a cluster and prompts you to add disks to Ceph.

When the initialization is complete, you’ll have both a Ceph and a LXD cluster, and LXD itself will have been configured with both networking and storage suitable for use in a cluster.
