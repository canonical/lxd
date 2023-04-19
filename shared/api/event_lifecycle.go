package api

// Define consts for all the lifecycle events.
const (
	EventLifecycleCertificateCreated                = "certificate-created"
	EventLifecycleCertificateDeleted                = "certificate-deleted"
	EventLifecycleCertificateUpdated                = "certificate-updated"
	EventLifecycleClusterCertificateUpdated         = "cluster-certificate-updated"
	EventLifecycleClusterDisabled                   = "cluster-disabled"
	EventLifecycleClusterEnabled                    = "cluster-enabled"
	EventLifecycleClusterGroupCreated               = "cluster-group-created"
	EventLifecycleClusterGroupDeleted               = "cluster-group-deleted"
	EventLifecycleClusterGroupRenamed               = "cluster-group-renamed"
	EventLifecycleClusterGroupUpdated               = "cluster-group-updated"
	EventLifecycleClusterMemberAdded                = "cluster-member-added"
	EventLifecycleClusterMemberRemoved              = "cluster-member-removed"
	EventLifecycleClusterMemberRenamed              = "cluster-member-renamed"
	EventLifecycleClusterMemberUpdated              = "cluster-member-updated"
	EventLifecycleClusterTokenCreated               = "cluster-token-created"
	EventLifecycleConfigUpdated                     = "config-updated"
	EventLifecycleDeploymentCreated                 = "deployment-created"
	EventLifecycleDeploymentDeleted                 = "deployment-deleted"
	EventLifecycleDeploymentRenamed                 = "deployment-renamed"
	EventLifecycleDeploymentUpdated                 = "deployment-updated"
	EventLifecycleDeploymentInstanceSetCreated      = "deployment-instance-set-created"
	EventLifecycleDeploymentInstanceSetDeleted      = "deployment-instance-set-deleted"
	EventLifecycleDeploymentInstanceSetRenamed      = "deployment-instance-set-renamed"
	EventLifecycleDeploymentInstanceSetUpdated      = "deployment-instance-set-updated"
	EventLifecycleImageAliasCreated                 = "image-alias-created"
	EventLifecycleImageAliasDeleted                 = "image-alias-deleted"
	EventLifecycleImageAliasRenamed                 = "image-alias-renamed"
	EventLifecycleImageAliasUpdated                 = "image-alias-updated"
	EventLifecycleImageCreated                      = "image-created"
	EventLifecycleImageDeleted                      = "image-deleted"
	EventLifecycleImageRefreshed                    = "image-refreshed"
	EventLifecycleImageRetrieved                    = "image-retrieved"
	EventLifecycleImageSecretCreated                = "image-secret-created"
	EventLifecycleImageUpdated                      = "image-updated"
	EventLifecycleInstanceBackupCreated             = "instance-backup-created"
	EventLifecycleInstanceBackupDeleted             = "instance-backup-deleted"
	EventLifecycleInstanceBackupRenamed             = "instance-backup-renamed"
	EventLifecycleInstanceBackupRetrieved           = "instance-backup-retrieved"
	EventLifecycleInstanceConsole                   = "instance-console"
	EventLifecycleInstanceConsoleReset              = "instance-console-reset"
	EventLifecycleInstanceConsoleRetrieved          = "instance-console-retrieved"
	EventLifecycleInstanceCreated                   = "instance-created"
	EventLifecycleInstanceDeleted                   = "instance-deleted"
	EventLifecycleInstanceExec                      = "instance-exec"
	EventLifecycleInstanceFileDeleted               = "instance-file-deleted"
	EventLifecycleInstanceFilePushed                = "instance-file-pushed"
	EventLifecycleInstanceFileRetrieved             = "instance-file-retrieved"
	EventLifecycleInstanceLogDeleted                = "instance-log-deleted"
	EventLifecycleInstanceLogRetrieved              = "instance-log-retrieved"
	EventLifecycleInstanceMetadataRetrieved         = "instance-metadata-retrieved"
	EventLifecycleInstanceMetadataTemplateCreated   = "instance-metadata-template-created"
	EventLifecycleInstanceMetadataTemplateDeleted   = "instance-metadata-template-deleted"
	EventLifecycleInstanceMetadataTemplateRetrieved = "instance-metadata-template-retrieved"
	EventLifecycleInstanceMetadataUpdated           = "instance-metadata-updated"
	EventLifecycleInstancePaused                    = "instance-paused"
	EventLifecycleInstanceReady                     = "instance-ready"
	EventLifecycleInstanceRenamed                   = "instance-renamed"
	EventLifecycleInstanceRestarted                 = "instance-restarted"
	EventLifecycleInstanceRestored                  = "instance-restored"
	EventLifecycleInstanceResumed                   = "instance-resumed"
	EventLifecycleInstanceShutdown                  = "instance-shutdown"
	EventLifecycleInstanceSnapshotCreated           = "instance-snapshot-created"
	EventLifecycleInstanceSnapshotDeleted           = "instance-snapshot-deleted"
	EventLifecycleInstanceSnapshotRenamed           = "instance-snapshot-renamed"
	EventLifecycleInstanceSnapshotUpdated           = "instance-snapshot-updated"
	EventLifecycleInstanceStarted                   = "instance-started"
	EventLifecycleInstanceStopped                   = "instance-stopped"
	EventLifecycleInstanceUpdated                   = "instance-updated"
	EventLifecycleNetworkACLCreated                 = "network-acl-created"
	EventLifecycleNetworkACLDeleted                 = "network-acl-deleted"
	EventLifecycleNetworkACLRenamed                 = "network-acl-renamed"
	EventLifecycleNetworkACLUpdated                 = "network-acl-updated"
	EventLifecycleNetworkCreated                    = "network-created"
	EventLifecycleNetworkDeleted                    = "network-deleted"
	EventLifecycleNetworkForwardCreated             = "network-forward-created"
	EventLifecycleNetworkForwardDeleted             = "network-forward-deleted"
	EventLifecycleNetworkForwardUpdated             = "network-forward-updated"
	EventLifecycleNetworkLoadBalancerCreated        = "network-load-balancer-created"
	EventLifecycleNetworkLoadBalancerDeleted        = "network-load-balancer-deleted"
	EventLifecycleNetworkLoadBalancerUpdated        = "network-load-balancer-updated"
	EventLifecycleNetworkPeerCreated                = "network-peer-created"
	EventLifecycleNetworkPeerDeleted                = "network-peer-deleted"
	EventLifecycleNetworkPeerUpdated                = "network-peer-updated"
	EventLifecycleNetworkRenamed                    = "network-renamed"
	EventLifecycleNetworkUpdated                    = "network-updated"
	EventLifecycleNetworkZoneCreated                = "network-zone-created"
	EventLifecycleNetworkZoneDeleted                = "network-zone-deleted"
	EventLifecycleNetworkZoneRecordCreated          = "network-zone-record-created"
	EventLifecycleNetworkZoneRecordDeleted          = "network-zone-record-deleted"
	EventLifecycleNetworkZoneRecordUpdated          = "network-zone-record-updated"
	EventLifecycleNetworkZoneUpdated                = "network-zone-updated"
	EventLifecycleOperationCancelled                = "operation-cancelled"
	EventLifecycleProfileCreated                    = "profile-created"
	EventLifecycleProfileDeleted                    = "profile-deleted"
	EventLifecycleProfileRenamed                    = "profile-renamed"
	EventLifecycleProfileUpdated                    = "profile-updated"
	EventLifecycleProjectCreated                    = "project-created"
	EventLifecycleProjectDeleted                    = "project-deleted"
	EventLifecycleProjectRenamed                    = "project-renamed"
	EventLifecycleProjectUpdated                    = "project-updated"
	EventLifecycleStoragePoolCreated                = "storage-pool-created"
	EventLifecycleStoragePoolDeleted                = "storage-pool-deleted"
	EventLifecycleStoragePoolUpdated                = "storage-pool-updated"
	EventLifecycleStorageBucketCreated              = "storage-bucket-created"
	EventLifecycleStorageBucketUpdated              = "storage-bucket-updated"
	EventLifecycleStorageBucketDeleted              = "storage-bucket-deleted"
	EventLifecycleStorageBucketKeyCreated           = "storage-bucket-key-created"
	EventLifecycleStorageBucketKeyUpdated           = "storage-bucket-key-updated"
	EventLifecycleStorageBucketKeyDeleted           = "storage-bucket-key-deleted"
	EventLifecycleStorageVolumeCreated              = "storage-volume-created"
	EventLifecycleStorageVolumeBackupCreated        = "storage-volume-backup-created"
	EventLifecycleStorageVolumeBackupDeleted        = "storage-volume-backup-deleted"
	EventLifecycleStorageVolumeBackupRenamed        = "storage-volume-backup-renamed"
	EventLifecycleStorageVolumeBackupRetrieved      = "storage-volume-backup-retrieved"
	EventLifecycleStorageVolumeDeleted              = "storage-volume-deleted"
	EventLifecycleStorageVolumeRenamed              = "storage-volume-renamed"
	EventLifecycleStorageVolumeRestored             = "storage-volume-restored"
	EventLifecycleStorageVolumeSnapshotCreated      = "storage-volume-snapshot-created"
	EventLifecycleStorageVolumeSnapshotDeleted      = "storage-volume-snapshot-deleted"
	EventLifecycleStorageVolumeSnapshotRenamed      = "storage-volume-snapshot-renamed"
	EventLifecycleStorageVolumeSnapshotUpdated      = "storage-volume-snapshot-updated"
	EventLifecycleStorageVolumeUpdated              = "storage-volume-updated"
	EventLifecycleWarningAcknowledged               = "warning-acknowledged"
	EventLifecycleWarningDeleted                    = "warning-deleted"
	EventLifecycleWarningReset                      = "warning-reset"
)
