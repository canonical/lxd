package api

// BackupMetadataVersion represents the backup file format version.
type BackupMetadataVersion uint

const (
	// BackupMetadataVersion1 represents the original backup file format version.
	BackupMetadataVersion1 BackupMetadataVersion = 1

	// BackupMetadataVersion2 represents the updated backup file format version which is using
	// restructured fields in order to be able to track custom storage volumes attached to the instance.
	BackupMetadataVersion2 BackupMetadataVersion = 2
)
