:relatedlinks: [Run&#32;system&#32;containers&#32;with&#32;LXD](https://canonical.com/lxd), [Open&#32;source&#32;for&#32;beginners:&#32;setting&#32;up&#32;your&#32;dev&#32;environment&#32;with&#32;LXD](https://ubuntu.com/blog/open-source-for-beginners-dev-environment-with-lxd)

.. meta::
   :description: LXD home page

.. _lxd-homepage:

LXD
===

LXD (|lxd-listen-anchor-open|\ ``[lɛks'di:]``\ |lxd-listen-anchor-close|) is a modern, secure and powerful system container and virtual machine manager.

.. raw:: html

   <audio id="player"><source src="_static/lxd.mp3" type="audio/mpeg"></audio>

.. |lxd-listen-anchor-open| raw:: html

   <a href="#" title="Listen" onclick="document.getElementById('player').play();return false;">

.. |lxd-listen-anchor-close| raw:: html

   &#128264;</a>

.. include:: ../README.md
   :parser: myst_parser.docutils_
   :start-after: <!-- Include start LXD intro -->
   :end-before: <!-- Include end LXD intro -->

----

.. toctree::
   :hidden:
   :titlesonly:

   self
   Tutorial <tutorial/first_steps>
   howto/index
   explanation/index
   reference/index

.. only:: html

   In this documentation
   ---------------------

   Start here
   ~~~~~~~~~~

   Follow the tutorial for a guided introduction to LXD, and refer to these guides for installation and initialization instructions.

   .. domain::

      .. slice:: Get started

         :doc:`Tutorial <tutorial/first_steps>`
         :doc:`Installation <installing>`
         :doc:`Initialization <howto/initialize>`
         :doc:`Preseed file fields for non-interactive configuration <reference/preseed_yaml_fields>`

   System containers and virtual machines
   ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

   LXD runs workloads on system containers or virtual machines.
   These instances are created using images and can be grouped under projects.

   .. domain::

      .. slice:: Instances

         :doc:`About containers and virtual machines <explanation/instances>` slice
         :doc:`Create <howto/instances_create>` slice
         :doc:`Configure <howto/instances_configure>` slice
         :doc:`Manage <howto/instances_manage>` slice
         :doc:`Back up <howto/instances_backup>` slice
         :doc:`Migrate <howto/instances_migrate>` slice
         :doc:`Import <howto/import_machines_to_instances>` slice
         :doc:`Live migration <howto/instances_migrate>`
         :doc:`Guest OS compatibility matrix <guest-os-compatibility>`
         :doc:`Configuration options <reference/instance_options>`
         :doc:`Store configuration options in profiles <profiles>`
         :doc:`Automate configuration with cloud-init <cloud-init>`

      .. slice:: Images

         :doc:`About local and remote images <image-handling>`
         :doc:`Remote image servers <reference/remote_image_servers>`
         :doc:`Manage images <howto/images_manage>`
         :doc:`Image format <reference/image_format>`

      .. slice:: Projects

         :doc:`Overview <explanation/projects>` slice
         :doc:`Create and configure <howto/projects_create>` slice
         :doc:`Confine users to projects <howto/projects_confine>`
         :doc:`Configuration options <reference/projects>` slice

   Clusters
   ~~~~~~~~

   LXD servers can be joined together into clusters, and clusters can be connected through cluster links.

   .. domain::

      .. slice:: Clusters and cluster members

         :doc:`Overview <explanation/clusters>` slice
         :doc:`Form a cluster <howto/cluster_form>`
         :doc:`Use placement groups to distribute instances across a cluster <howto/cluster_placement_groups>`
         :doc:`Recover a cluster <howto/cluster_recover>`
         :doc:`Set up a highly available virtual IP <howto/cluster_vip>`
         :doc:`Cluster member configuration options <reference/cluster_member_config>`

      .. slice:: Multiple clusters

         :doc:`Create cluster links <howto/cluster_links_create>`
         :doc:`Manage cluster links <howto/cluster_links_manage>`
         :doc:`Cluster link configuration options <reference/cluster_link_config>`
         :doc:`Create replicators <howto/replicators_create>`
         :doc:`Manage replicators <howto/replicators_manage>`
         :doc:`Replicator configuration options <reference/replicator_config>`

   Storage and networks
   ~~~~~~~~~~~~~~~~~~~~

   Each LXD server is configured with storage and network options.
   These guides will help you understand and work with these resources.

   .. domain::

      .. slice:: Storage

         :doc:`Overview <explanation/storage>` slice
         :doc:`Driver types and configuration options <reference/storage_drivers>`
         :doc:`Manage pools <howto/storage_pools>`
         :doc:`Manage volumes <howto/storage_volumes>`
         :doc:`Manage buckets <howto/storage_buckets>`
         :doc:`Create or move instances in a pool <howto/storage_create_instance>`
         :doc:`Back up volumes <howto/storage_backup_volume>`
         :doc:`Move or copy volumes <howto/storage_move_volume>`

      .. slice:: Networking

         :doc:`Networking setups <explanation/networks>`
         :doc:`Network types and configuration options <reference/networks>`
         :doc:`Create <howto/network_create>` slice
         :doc:`Configure networks <howto/network_configure>`
         :doc:`Configure LXD as a BGP server <howto/network_bgp>`
         :doc:`Configure ACLs <howto/network_acls>`
         :doc:`Configure forwards <howto/network_forwards>`
         :doc:`Configure network zones <howto/network_zones>`
         :doc:`Set up OVN <howto/network_ovn_setup>`
         :doc:`Configure load balancers <howto/network_load_balancers>`
         :doc:`Configure peer routing <howto/network_ovn_peers>`

   Access LXD
   ~~~~~~~~~~

   These guides help you to access and communicate with LXD.

   .. domain::

      .. slice:: Server

         :doc:`Expose the server to the network <howto/server_expose>` 
         :doc:`Server configuration options <server>`
         :doc:`Production server settings <reference/server_settings>`
         :doc:`Supported server architectures <architectures>`

      .. slice:: Authentication

         :doc:`Overview <authentication>` slice
         :doc:`Access the graphical UI <howto/access_ui>`
         :doc:`Use single sign-on with OIDC <howto/oidc>`
         :doc:`Use bearer tokens <howto/auth_bearer>`
         :doc:`Add remote servers <remotes>`
         :doc:`Authenticate to the DevLXD API <howto/devlxd_authenticate>`

      .. slice:: Authorization

         :doc:`Overview <explanation/authorization>` slice
         :doc:`Permissions reference <reference/permissions>`

      .. slice:: Client-server communication

         :doc:`REST API reference <restapi_landing>`
         :doc:`lxc CLI man pages <reference/manpages/lxc>`
         :doc:`About the lxd-lxc CLIs <explanation/lxd_lxc>`
         :doc:`DevLXD API reference <dev-lxd>`

   Quality
   ~~~~~~~

   Follow these guides to secure your LXD deployment and optimize its performance.

   .. domain::

      .. slice:: Security

         :doc:`Overview <explanation/security>` slice
         :doc:`Harden security <howto/security_harden>`
         :doc:`Monitor security events <howto/security_events>`

      .. slice:: Performance

         :doc:`Benchmark performance <howto/benchmark_performance>`
         :doc:`Performance tuning <explanation/performance_tuning>`
         :doc:`Increase network bandwidth <howto/network_increase_bandwidth>`

      .. slice:: Monitoring

         :doc:`Monitor metrics <metrics>`
         :doc:`Send logs to Loki <howto/logs_loki>`
         :doc:`Visualize metrics and logs with Grafana <howto/grafana>`
         :doc:`Metrics reference <reference/provided_metrics>`
         :doc:`Events reference <events>`

   Lifecycle and administration
   ~~~~~~~~~~~~~~~~~~~~~~~~~~~~

   These guides cover lifecycle and ongoing administration concerns.

   .. domain::

      .. slice:: Update and upgrade

         :doc:`Snap updates and upgrades <howto/snap>`
         :doc:`Releases and snap reference <reference/releases-snap>` 
         :doc:`Release notes <reference/release-notes/index>`
         :doc:`Deprecation notices <reference/deprecation_notices>`
         :doc:`Decommission LXD <howto/decommission>`

      .. slice:: Troubleshooting

         :doc:`Configure a firewall <howto/network_bridge_firewalld>`
         :doc:`Troubleshoot instances <howto/instances_troubleshoot>`
         :doc:`Troubleshoot networks <howto/network_ipam>`
         :doc:`Troubleshoot Dqlite <howto/dqlite_troubleshoot>`
         :doc:`Debug LXD <debugging>`
         :doc:`Track a bugfix in the LXD snap <howto/snap_track_fix>`
         :doc:`Frequently asked questions <faq>`

      .. slice:: Disaster recovery

         :doc:`Perform disaster recovery with replicators <howto/replicators_dr>`
         :doc:`Back up a server <backup>`
         :doc:`Recover LXD database records <howto/disaster_recovery>`
         :doc:`Disaster recovery with storage replication <howto/disaster_recovery_replication>`

How this documentation is organized
-----------------------------------

This documentation uses the Diátaxis documentation structure.

- The :ref:`Tutorial <first-steps>` takes you step-by-step through installing and initializing LXD, and learning how to use basic features such as launching instances.
- The :ref:`howtos` assume you have basic familiarity with LXD. They walk you through specific tasks, such as creating storage pools and managing clusters.
- The :ref:`reference` guides include configuration options, API references, and other technical details.
- The :ref:`explanation` section includes topic overviews and detailed explanations of key concepts, such as the difference between system containers and virtual machines.

Project and community
---------------------

LXD is a member of the `Canonical <https://canonical.com>`_ family.
It’s an open source project that warmly welcomes community contributions, suggestions, fixes, and constructive feedback.

Get involved
~~~~~~~~~~~~

* :ref:`Support <support>`
* `Discussion forum <https://discourse.ubuntu.com/c/project/lxd/126>`_
* :ref:`Contribute <howto-contribute>`
* `YouTube channel <https://www.youtube.com/c/LXDvideos>`_

Releases
~~~~~~~~

* :ref:`ref-release-notes`
* `Release tarballs <https://github.com/canonical/lxd/releases/>`_

Governance and policies
~~~~~~~~~~~~~~~~~~~~~~~

* `Code of conduct <https://ubuntu.com/community/docs/ethos/code-of-conduct>`_

Commercial support
~~~~~~~~~~~~~~~~~~

Thinking about using LXD for your next project? `Get in touch <https://canonical.com/contact-us>`_!
