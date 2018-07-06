package lxd

import (
	"io"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
)

// The Operation type represents a currently running operation.
type Operation interface {
	AddHandler(function func(api.Operation)) (target *EventTarget, err error)
	Cancel() (err error)
	Get() (op api.Operation)
	GetWebsocket(secret string) (conn *websocket.Conn, err error)
	RemoveHandler(target *EventTarget) (err error)
	Refresh() (err error)
	Wait() (err error)
}

// The RemoteOperation type represents an Operation that may be using multiple servers.
type RemoteOperation interface {
	AddHandler(function func(api.Operation)) (target *EventTarget, err error)
	CancelTarget() (err error)
	GetTarget() (op *api.Operation, err error)
	Wait() (err error)
}

// The Server type represents a generic read-only server.
type Server interface {
	GetConnectionInfo() (info *ConnectionInfo, err error)
	GetHTTPClient() (client *http.Client, err error)
}

// The ImageServer type represents a read-only image server.
type ImageServer interface {
	Server

	// Image handling functions
	GetImages() (images []api.Image, err error)
	GetImageFingerprints() (fingerprints []string, err error)

	GetImage(fingerprint string) (image *api.Image, ETag string, err error)
	GetImageFile(fingerprint string, req ImageFileRequest) (resp *ImageFileResponse, err error)
	GetImageSecret(fingerprint string) (secret string, err error)

	GetPrivateImage(fingerprint string, secret string) (image *api.Image, ETag string, err error)
	GetPrivateImageFile(fingerprint string, secret string, req ImageFileRequest) (resp *ImageFileResponse, err error)

	GetImageAliases() (aliases []api.ImageAliasesEntry, err error)
	GetImageAliasNames() (names []string, err error)

	GetImageAlias(name string) (alias *api.ImageAliasesEntry, ETag string, err error)
}

// The ContainerServer type represents a full featured LXD server.
type ContainerServer interface {
	ImageServer

	// Server functions
	GetServer() (server *api.Server, ETag string, err error)
	GetServerResources() (resources *api.Resources, err error)
	UpdateServer(server api.ServerPut, ETag string) (err error)
	HasExtension(extension string) (exists bool)
	RequireAuthenticated(authenticated bool)
	IsClustered() (clustered bool)
	UseTarget(name string) (client ContainerServer)

	// Certificate functions
	GetCertificateFingerprints() (fingerprints []string, err error)
	GetCertificates() (certificates []api.Certificate, err error)
	GetCertificate(fingerprint string) (certificate *api.Certificate, ETag string, err error)
	CreateCertificate(certificate api.CertificatesPost) (err error)
	UpdateCertificate(fingerprint string, certificate api.CertificatePut, ETag string) (err error)
	DeleteCertificate(fingerprint string) (err error)

	// Container functions
	GetContainerNames() (names []string, err error)
	GetContainers() (containers []api.Container, err error)
	GetContainer(name string) (container *api.Container, ETag string, err error)
	CreateContainer(container api.ContainersPost) (op Operation, err error)
	CreateContainerFromImage(source ImageServer, image api.Image, imgcontainer api.ContainersPost) (op RemoteOperation, err error)
	CopyContainer(source ContainerServer, container api.Container, args *ContainerCopyArgs) (op RemoteOperation, err error)
	UpdateContainer(name string, container api.ContainerPut, ETag string) (op Operation, err error)
	RenameContainer(name string, container api.ContainerPost) (op Operation, err error)
	MigrateContainer(name string, container api.ContainerPost) (op Operation, err error)
	DeleteContainer(name string) (op Operation, err error)

	ExecContainer(containerName string, exec api.ContainerExecPost, args *ContainerExecArgs) (op Operation, err error)
	ConsoleContainer(containerName string, console api.ContainerConsolePost, args *ContainerConsoleArgs) (op Operation, err error)
	GetContainerConsoleLog(containerName string, args *ContainerConsoleLogArgs) (content io.ReadCloser, err error)
	DeleteContainerConsoleLog(containerName string, args *ContainerConsoleLogArgs) (err error)

	GetContainerFile(containerName string, path string) (content io.ReadCloser, resp *ContainerFileResponse, err error)
	CreateContainerFile(containerName string, path string, args ContainerFileArgs) (err error)
	DeleteContainerFile(containerName string, path string) (err error)

	GetContainerSnapshotNames(containerName string) (names []string, err error)
	GetContainerSnapshots(containerName string) (snapshots []api.ContainerSnapshot, err error)
	GetContainerSnapshot(containerName string, name string) (snapshot *api.ContainerSnapshot, ETag string, err error)
	CreateContainerSnapshot(containerName string, snapshot api.ContainerSnapshotsPost) (op Operation, err error)
	CopyContainerSnapshot(source ContainerServer, snapshot api.ContainerSnapshot, args *ContainerSnapshotCopyArgs) (op RemoteOperation, err error)
	RenameContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPost) (op Operation, err error)
	MigrateContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPost) (op Operation, err error)
	DeleteContainerSnapshot(containerName string, name string) (op Operation, err error)

	GetContainerBackupNames(containerName string) (names []string, err error)
	GetContainerBackups(containername string) (backups []api.ContainerBackup, err error)
	GetContainerBackup(containerName string, name string) (backup *api.ContainerBackup, ETag string, err error)
	CreateContainerBackup(containerName string, backup api.ContainerBackupsPost) (op Operation, err error)
	RenameContainerBackup(containerName string, name string, backup api.ContainerBackupPost) (op Operation, err error)
	DeleteContainerBackup(containerName string, name string) (op Operation, err error)
	GetContainerBackupFile(containerName string, name string, req *BackupFileRequest) (resp *BackupFileResponse, err error)
	CreateContainerFromBackup(args ContainerBackupArgs) (op Operation, err error)

	GetContainerState(name string) (state *api.ContainerState, ETag string, err error)
	UpdateContainerState(name string, state api.ContainerStatePut, ETag string) (op Operation, err error)

	GetContainerLogfiles(name string) (logfiles []string, err error)
	GetContainerLogfile(name string, filename string) (content io.ReadCloser, err error)
	DeleteContainerLogfile(name string, filename string) (err error)

	GetContainerMetadata(name string) (metadata *api.ImageMetadata, ETag string, err error)
	SetContainerMetadata(name string, metadata api.ImageMetadata, ETag string) (err error)

	GetContainerTemplateFiles(containerName string) (templates []string, err error)
	GetContainerTemplateFile(containerName string, templateName string) (content io.ReadCloser, err error)
	CreateContainerTemplateFile(containerName string, templateName string, content io.ReadSeeker) (err error)
	UpdateContainerTemplateFile(containerName string, templateName string, content io.ReadSeeker) (err error)
	DeleteContainerTemplateFile(name string, templateName string) (err error)

	// Event handling functions
	GetEvents() (listener *EventListener, err error)

	// Image functions
	CreateImage(image api.ImagesPost, args *ImageCreateArgs) (op Operation, err error)
	CopyImage(source ImageServer, image api.Image, args *ImageCopyArgs) (op RemoteOperation, err error)
	UpdateImage(fingerprint string, image api.ImagePut, ETag string) (err error)
	DeleteImage(fingerprint string) (op Operation, err error)
	RefreshImage(fingerprint string) (op Operation, err error)
	CreateImageSecret(fingerprint string) (op Operation, err error)
	CreateImageAlias(alias api.ImageAliasesPost) (err error)
	UpdateImageAlias(name string, alias api.ImageAliasesEntryPut, ETag string) (err error)
	RenameImageAlias(name string, alias api.ImageAliasesEntryPost) (err error)
	DeleteImageAlias(name string) (err error)

	// Network functions ("network" API extension)
	GetNetworkNames() (names []string, err error)
	GetNetworks() (networks []api.Network, err error)
	GetNetwork(name string) (network *api.Network, ETag string, err error)
	GetNetworkLeases(name string) (leases []api.NetworkLease, err error)
	GetNetworkState(name string) (state *api.NetworkState, err error)
	CreateNetwork(network api.NetworksPost) (err error)
	UpdateNetwork(name string, network api.NetworkPut, ETag string) (err error)
	RenameNetwork(name string, network api.NetworkPost) (err error)
	DeleteNetwork(name string) (err error)

	// Operation functions
	GetOperationUUIDs() (uuids []string, err error)
	GetOperations() (operations []api.Operation, err error)
	GetOperation(uuid string) (op *api.Operation, ETag string, err error)
	GetOperationWait(uuid string, timeout int) (op *api.Operation, ETag string, err error)
	GetOperationWebsocket(uuid string, secret string) (conn *websocket.Conn, err error)
	DeleteOperation(uuid string) (err error)

	// Profile functions
	GetProfileNames() (names []string, err error)
	GetProfiles() (profiles []api.Profile, err error)
	GetProfile(name string) (profile *api.Profile, ETag string, err error)
	CreateProfile(profile api.ProfilesPost) (err error)
	UpdateProfile(name string, profile api.ProfilePut, ETag string) (err error)
	RenameProfile(name string, profile api.ProfilePost) (err error)
	DeleteProfile(name string) (err error)

	// Storage pool functions ("storage" API extension)
	GetStoragePoolNames() (names []string, err error)
	GetStoragePools() (pools []api.StoragePool, err error)
	GetStoragePool(name string) (pool *api.StoragePool, ETag string, err error)
	GetStoragePoolResources(name string) (resources *api.ResourcesStoragePool, err error)
	CreateStoragePool(pool api.StoragePoolsPost) (err error)
	UpdateStoragePool(name string, pool api.StoragePoolPut, ETag string) (err error)
	DeleteStoragePool(name string) (err error)

	// Storage volume functions ("storage" API extension)
	GetStoragePoolVolumeNames(pool string) (names []string, err error)
	GetStoragePoolVolumes(pool string) (volumes []api.StorageVolume, err error)
	GetStoragePoolVolume(pool string, volType string, name string) (volume *api.StorageVolume, ETag string, err error)
	CreateStoragePoolVolume(pool string, volume api.StorageVolumesPost) (err error)
	UpdateStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePut, ETag string) (err error)
	DeleteStoragePoolVolume(pool string, volType string, name string) (err error)
	RenameStoragePoolVolume(pool string, volType string, name string, volume api.StorageVolumePost) (err error)
	CopyStoragePoolVolume(pool string, source ContainerServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeCopyArgs) (op RemoteOperation, err error)
	MoveStoragePoolVolume(pool string, source ContainerServer, sourcePool string, volume api.StorageVolume, args *StoragePoolVolumeMoveArgs) (op RemoteOperation, err error)
	MigrateStoragePoolVolume(pool string, volume api.StorageVolumePost) (op Operation, err error)

	// Cluster functions ("cluster" API extensions)
	GetCluster() (cluster *api.Cluster, ETag string, err error)
	UpdateCluster(cluster api.ClusterPut, ETag string) (op Operation, err error)
	DeleteClusterMember(name string, force bool) (err error)
	GetClusterMemberNames() (names []string, err error)
	GetClusterMembers() (members []api.ClusterMember, err error)
	GetClusterMember(name string) (member *api.ClusterMember, ETag string, err error)
	RenameClusterMember(name string, member api.ClusterMemberPost) (err error)

	// Internal functions (for internal use)
	RawQuery(method string, path string, data interface{}, queryETag string) (resp *api.Response, ETag string, err error)
	RawWebsocket(path string) (conn *websocket.Conn, err error)
	RawOperation(method string, path string, data interface{}, queryETag string) (op Operation, ETag string, err error)
}

// The ConnectionInfo struct represents general information for a connection
type ConnectionInfo struct {
	Addresses   []string
	Certificate string
	Protocol    string
	URL         string
}

// The ContainerBackupArgs struct is used when creating a container from a backup
type ContainerBackupArgs struct {
	// The backup file
	BackupFile io.Reader
}

// The BackupFileRequest struct is used for a backup download request
type BackupFileRequest struct {
	// Writer for the backup file
	BackupFile io.WriteSeeker

	// Progress handler (called whenever some progress is made)
	ProgressHandler func(progress ioprogress.ProgressData)

	// A canceler that can be used to interrupt some part of the image download request
	Canceler *cancel.Canceler
}

// The BackupFileResponse struct is used as the response for backup downloads
type BackupFileResponse struct {
	// Size of backup file
	Size int64
}

// The ImageCreateArgs struct is used for direct image upload
type ImageCreateArgs struct {
	// Reader for the meta file
	MetaFile io.Reader

	// Filename for the meta file
	MetaName string

	// Reader for the rootfs file
	RootfsFile io.Reader

	// Filename for the rootfs file
	RootfsName string

	// Progress handler (called with upload progress)
	ProgressHandler func(progress ioprogress.ProgressData)
}

// The ImageFileRequest struct is used for an image download request
type ImageFileRequest struct {
	// Writer for the metadata file
	MetaFile io.WriteSeeker

	// Writer for the rootfs file
	RootfsFile io.WriteSeeker

	// Progress handler (called whenever some progress is made)
	ProgressHandler func(progress ioprogress.ProgressData)

	// A canceler that can be used to interrupt some part of the image download request
	Canceler *cancel.Canceler

	// Path retriever for image delta downloads
	// If set, it must return the path to the image file or an empty string if not available
	DeltaSourceRetriever func(fingerprint string, file string) string
}

// The ImageFileResponse struct is used as the response for image downloads
type ImageFileResponse struct {
	// Filename for the metadata file
	MetaName string

	// Size of the metadata file
	MetaSize int64

	// Filename for the rootfs file
	RootfsName string

	// Size of the rootfs file
	RootfsSize int64
}

// The ImageCopyArgs struct is used to pass additional options during image copy
type ImageCopyArgs struct {
	// Aliases to add to the copied image.
	Aliases []api.ImageAlias

	// Whether to have LXD keep this image up to date
	AutoUpdate bool

	// Whether to copy the source image aliases to the target
	CopyAliases bool

	// Whether this image is to be made available to unauthenticated users
	Public bool
}

// The StoragePoolVolumeCopyArgs struct is used to pass additional options
// during storage volume copy
type StoragePoolVolumeCopyArgs struct {
	// New name for the target
	Name string

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string
}

// The StoragePoolVolumeMoveArgs struct is used to pass additional options
// during storage volume move
type StoragePoolVolumeMoveArgs struct {
	StoragePoolVolumeCopyArgs
}

// The ContainerCopyArgs struct is used to pass additional options during container copy
type ContainerCopyArgs struct {
	// If set, the container will be renamed on copy
	Name string

	// If set, the container running state will be transferred (live migration)
	Live bool

	// If set, only the container will copied, its snapshots won't
	ContainerOnly bool

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string
}

// The ContainerSnapshotCopyArgs struct is used to pass additional options during container copy
type ContainerSnapshotCopyArgs struct {
	// If set, the container will be renamed on copy
	Name string

	// The transfer mode, can be "pull" (default), "push" or "relay"
	Mode string

	// API extension: container_snapshot_stateful_migration
	// If set, the container running state will be transferred (live migration)
	Live bool
}

// The ContainerConsoleArgs struct is used to pass additional options during a
// container console session
type ContainerConsoleArgs struct {
	// Bidirectional fd to pass to the container
	Terminal io.ReadWriteCloser

	// Control message handler (window resize)
	Control func(conn *websocket.Conn)

	// Closing this Channel causes a disconnect from the container's console
	ConsoleDisconnect chan bool
}

// The ContainerConsoleLogArgs struct is used to pass additional options during a
// container console log request
type ContainerConsoleLogArgs struct {
}

// The ContainerExecArgs struct is used to pass additional options during container exec
type ContainerExecArgs struct {
	// Standard input
	Stdin io.ReadCloser

	// Standard output
	Stdout io.WriteCloser

	// Standard error
	Stderr io.WriteCloser

	// Control message handler (window resize, signals, ...)
	Control func(conn *websocket.Conn)

	// Channel that will be closed when all data operations are done
	DataDone chan bool
}

// The ContainerFileArgs struct is used to pass the various options for a container file upload
type ContainerFileArgs struct {
	// File content
	Content io.ReadSeeker

	// User id that owns the file
	UID int64

	// Group id that owns the file
	GID int64

	// File permissions
	Mode int

	// File type (file or directory)
	Type string

	// File write mode (overwrite or append)
	WriteMode string
}

// The ContainerFileResponse struct is used as part of the response for a container file download
type ContainerFileResponse struct {
	// User id that owns the file
	UID int64

	// Group id that owns the file
	GID int64

	// File permissions
	Mode int

	// File type (file or directory)
	Type string

	// If a directory, the list of files inside it
	Entries []string
}
