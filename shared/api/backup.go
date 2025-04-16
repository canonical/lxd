package api

const (
	// BackupMetadataVersion1 represents the original backup file format version.
	BackupMetadataVersion1 uint32 = 1

	// BackupMetadataVersion2 represents the updated backup file format version which is using
	// restructured fields in order to be able to track custom storage volumes attached to the instance.
	BackupMetadataVersion2 uint32 = 2
)
