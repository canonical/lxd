(cluster-form)=
# How to form a cluster

First you need to choose a bootstrap LXD node. It can be an existing
LXD server or a brand new one. Then you need to initialize the
bootstrap node and join further nodes to the cluster. This can be done
interactively or with a preseed file.

## Interactively

Run `lxd init` and answer `yes` to the very first question ("Would you
like to use LXD clustering?"). Then choose a name for identifying the
node, and an IP or DNS address that other nodes can use to connect to
it, and answer `no` to the question about whether you're joining an
existing cluster. Finally, optionally create a storage pool and a
network bridge. At this point your first cluster node should be up and
available on your network.

You can now join further nodes to the cluster. Note however that these
nodes should be brand new LXD servers, or alternatively you should
clear their contents before joining, since any existing data on them
will be lost.

There are two ways to add a member to an existing cluster; using the trust password or using a join token.
A join token for a new member is generated in advance on the existing cluster using the command:

```
lxc cluster add <new member name>
```

This will return a single-use join token which can then be used in the join token question stage of `lxd init`.
The join token contains the addresses of the existing online members, as well as a single-use secret and the
fingerprint of the cluster certificate. This reduces the amount of questions you have to answer during `lxd init`
as the join token can be used to answer these questions automatically.

Alternatively you can use the trust password instead of using a join token.

To add an additional node, run `lxd init` and answer `yes` to the question about whether to use clustering.
Choose a node name that is different from the one chosen for the bootstrap node or any other nodes you have joined
so far. Then pick an IP or DNS address for the node and answer `yes` to the question about whether you're joining
an existing cluster.

If you have a join token then answer `yes` to the question that asks if you have a join token and then copy it in
when it asks for it.

If you do not have a join token, but have a trust password instead then, then answer `no` to the question that asks
if you have a join token. Then pick an address of an existing node in the cluster and check the fingerprint that
gets printed matches the cluster certificate of the existing members.

## Preseed

Create a preseed file for the bootstrap node with the configuration
you want, for example:

```yaml
config:
  core.trust_password: sekret
  core.https_address: 10.55.60.171:8443
  images.auto_update_interval: 15
storage_pools:
- name: default
  driver: dir
networks:
- name: lxdbr0
  type: bridge
  config:
    ipv4.address: 192.168.100.14/24
    ipv6.address: none
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
  server_name: node1
  enabled: true
```

Then run `cat <preseed-file> | lxd init --preseed` and your first node
should be bootstrapped.

Now create a bootstrap file for another node. You only need to fill in the
``cluster`` section with data and configuration values that are specific to the joining
node.

Be sure to include the address and certificate of the target bootstrap node. To
create a YAML-compatible entry for the `cluster_certificate` key you can use a
command like `sed ':a;N;$!ba;s/\n/\n\n/g' /var/lib/lxd/cluster.crt` (or
`sed ':a;N;$!ba;s/\n/\n\n/g' /var/snap/lxd/common/lxd/cluster.crt` for snap users), which you
have to run on the bootstrap node. `cluster_certificate_path` key (which should
contain valid path to cluster certificate) can be used instead of `cluster_certificate` key.

For example:

```yaml
cluster:
  enabled: true
  server_name: node2
  server_address: 10.55.60.155:8443
  cluster_address: 10.55.60.171:8443
  cluster_certificate: "-----BEGIN CERTIFICATE-----

opyQ1VRpAg2sV2C4W8irbNqeUsTeZZxhLqp4vNOXXBBrSqUCdPu1JXADV0kavg1l

2sXYoMobyV3K+RaJgsr1OiHjacGiGCQT3YyNGGY/n5zgT/8xI0Dquvja0bNkaf6f

...

-----END CERTIFICATE-----
"
  cluster_password: sekret
  member_config:
  - entity: storage-pool
    name: default
    key: source
    value: ""
```

When joining a cluster using a cluster join token, the following fields can be omitted:

- `server_name`
- `cluster_address`
- `cluster_certificate`
- `cluster_password`

And instead the full token be passed through the `cluster_token` field.
