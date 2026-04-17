---
relatedlinks: "[Run&#32;system&#32;containers&#32;with&#32;LXD](https://canonical.com/lxd), [Open&#32;source&#32;for&#32;beginners:&#32;setting&#32;up&#32;your&#32;dev&#32;environment&#32;with&#32;LXD](https://ubuntu.com/blog/open-source-for-beginners-dev-environment-with-lxd)"
---

# LXD

LXD (<a href="#" title="Listen" onclick="document.getElementById('player').play();return false;">`[lɛks'di:]`&#128264;</a>) is a modern, secure and powerful system container and virtual machine manager.

<audio id="player"><source src="_static/lxd.mp3" type="audio/mpeg"></audio>

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start LXD intro -->
    :end-before: <!-- Include end LXD intro -->
```

---

## In this documentation

### Start here

Follow the tutorial for a guided introduction to LXD, including installing it using its snap.

- **Tutorial**: {ref}`Requirements <tutorial-requirements>` • {ref}`Install LXD using the snap <tutorial-install>` • {ref}`Create <tutorial-create-instances>` and {ref}`configure <tutorial-configure>` test instances • Learn how to {ref}`open an interactive shell <tutorial-shell>` into an instance • Learn how to {ref}`back up and restore instances <tutorial-snapshots>`

### Server and client

These guides help you manage a standalone LXD server or a cluster of servers, including how to access and communicate with servers.

- **Server**: {ref}`Server configuration options <server>` • {ref}`Expose the server to the network <server-expose>`  • {ref}`Supported server architectures <architectures>`
- **Clusters**: {ref}`About clusters <exp-clusters>` • {ref}`Form a cluster <cluster-form>` • {ref}`Use placement groups for instance distribution across a cluster <cluster-placement-groups>` • {ref}`Recover a cluster <cluster-recover>` • {ref}`Set up a highly available virtual IP <howto-cluster-vip>`
- **Access**: {ref}`Access the graphical UI <access-ui>` • {ref}`Authentication <authentication>` and {ref}`authorization <authorization>` • {ref}`Use single sign-on with OIDC <howto-oidc>` • {ref}`Use bearer tokens <howto-auth-bearer>` • {ref}`Permissions reference <permissions-reference>` • {ref}`Add remote servers <remotes>` • {ref}`Instances grouping with projects <exp-projects>`
- **Client-server communication**: {ref}`REST API reference <reference-api>` • {ref}`lxc CLI man pages <reference-manpages>` • About the {ref}`lxd-lxc` CLIs

### Workload management

An LXD server runs workloads on containers or virtual machines, which are created using images and can be grouped using projects.

- **Instances**: {ref}`System containers and virtual machines <containers-and-vms>` • {ref}`Guest OS compatibility matrix <guest-os-compatibility>` • {ref}`Create <instances-create>`, {ref}`configure <instances-configure>`, and {ref}`manage <instances-manage>` instances • {ref}`Configuration options <instance-options>` • {ref}`Store configuration options in profiles <profiles>` • {ref}`Automate configuration with cloud-init <cloud-init>` • {ref}`Back up <instances-backup>`, {ref}`migrate <howto-instances-migrate>`, and {ref}`import <import-machines-to-instances>` instances • {ref}`Live migration <live-migration>`
- **Images**: {ref}`About local and remote images <about-images>` • {ref}`List of remote image servers <remote-image-servers>` •  {ref}`Manage images <images-manage>`
- **Projects**: {ref}`Create and configure projects <projects-create>` • {ref}`Confine users to projects <projects-confine>` • {ref}`About grouping instances <exp-projects>`

### Storage and networks

Each LXD server is configured with storage and network options. These guides will help you understand and work with these resources.

- **Storage**: {ref}`Storage concepts <exp-storage>` • {ref}`Driver types and configuration options <storage-drivers>` • Manage {ref}`pools <howto-storage-pools>`, {ref}`volumes <howto-storage-volumes>`, and {ref}`buckets <howto-storage-buckets>` • {ref}`Back up volumes <howto-storage-backup-volume>` • {ref}`Move or copy volumes <howto-storage-move-volume>`
- **Networks**: {ref}`Networking setups <networks>` • {ref}`Network types and configuration options <network-types>` • {ref}`Create <network-create>` and {ref}`configure <network-configure>` networks • Configure {ref}`ACLs <network-acls>`, {ref}`forwards <network-forwards>`, and {ref}`load balancers <network-load-balancers>` • {ref}`Configure a firewall <network-bridge-firewall>`

### Lifecycle and administration

These guides cover lifecycle and ongoing administration concerns, such as installation (including non-snap options), production deployment setup, and security.

- **Lifecycle**: {ref}`Installation <installing>` • {ref}`Initialization <initialize>` • {ref}`Releases and snap reference <ref-releases-snap>`  • {ref}`Snap updates and upgrades <howto-snap-updates-upgrades>` • {ref}`ref-release-notes`
- **Production setup**: {ref}`Production server settings <reference-production>` • {ref}`Back up a server <backups>` • {ref}`Benchmark performance <benchmark-performance>` • {ref}`Monitor metrics <metrics>` • {ref}`Perform disaster recovery <disaster-recovery>` •{ref}`Performance tuning <performance-tuning>`
- **Security**: {ref}`Overview <security>` • {ref}`Harden security <howto-security-harden>` • {ref}`Instance security policies <instance-options-security>`

## How this documentation is organized

This documentation uses the Diátaxis documentation structure.

- The {ref}`Tutorial <first-steps>` takes you step-by-step through installing and initializing LXD, and learning how to use basic features such as launching instances.
- The {ref}`howtos` assume you have basic familiarity with LXD. They walk you through specific tasks, such as creating storage pools and managing clusters.
- The {ref}`reference` guides include configuration options, API references, and other technical details.
- The {ref}`explanation` section includes topic overviews and detailed explanations of key concepts, such as the difference between system containers and virtual machines.

## Project and community

LXD is a member of the [Canonical](https://canonical.com) family. It’s an open source project that warmly welcomes community contributions, suggestions, fixes, and constructive feedback.

### Get involved

- {ref}`Support <support>`
- [Discussion forum](https://discourse.ubuntu.com/c/project/lxd/126)
- {ref}`Contribute <howto-contribute>`
- [YouTube channel](https://www.youtube.com/c/LXDvideos)

### Releases

- {ref}`ref-release-notes`
- [Release tarballs](https://github.com/canonical/lxd/releases/)

### Governance and policies

- [Code of conduct](https://ubuntu.com/community/docs/ethos/code-of-conduct)

### Commercial support

Thinking about using LXD for your next project? [Get in touch](https://canonical.com/contact-us)!

```{toctree}
:hidden:
:titlesonly:

self
Tutorial <tutorial/first_steps>
howto/index
explanation/index
reference/index
```
