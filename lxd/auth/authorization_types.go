package auth

type Relation string

const (
	RelationImageManager         Relation = "image_manager"
	RelationImageViewer          Relation = "image_viewer"
	RelationInstanceManager      Relation = "instance_manager"
	RelationInstanceOperator     Relation = "instance_operator"
	RelationInstanceViewer       Relation = "instance_viewer"
	RelationManager              Relation = "manager"
	RelationMember               Relation = "member"
	RelationNetworkACLManager    Relation = "network_acl_manager"
	RelationNetworkACLViewer     Relation = "network_acl_viewer"
	RelationNetworkManager       Relation = "network_manager"
	RelationNetworkViewer        Relation = "network_viewer"
	RelationNetworkZoneManager   Relation = "network_zone_manager"
	RelationNetworkZoneViewer    Relation = "network_zone_viewer"
	RelationOperator             Relation = "operator"
	RelationProfileManager       Relation = "profile_manager"
	RelationProfileViewer        Relation = "profile_viewer"
	RelationProject              Relation = "project"
	RelationStorageBucketManager Relation = "storage_bucket_manager"
	RelationStorageBucketViewer  Relation = "storage_bucket_viewer"
	RelationStorageVolumeManager Relation = "storage_volume_manager"
	RelationStorageVolumeViewer  Relation = "storage_volume_viewer"
	RelationViewer               Relation = "viewer"
)

type ObjectType string

const (
	ObjectTypeGroup         ObjectType = "group"
	ObjectTypeImage         ObjectType = "image"
	ObjectTypeInstance      ObjectType = "instance"
	ObjectTypeNetwork       ObjectType = "network"
	ObjectTypeNetworkACL    ObjectType = "network_acl"
	ObjectTypeNetworkZone   ObjectType = "network_zone"
	ObjectTypeProfile       ObjectType = "profile"
	ObjectTypeProject       ObjectType = "project"
	ObjectTypeStorageBucket ObjectType = "storage_bucket"
	ObjectTypeStorageVolume ObjectType = "storage_volume"
	ObjectTypeUser          ObjectType = "user"
)
