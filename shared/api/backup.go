package api

// BackupVersion represents the backup file format version.
type BackupVersion string

const (
	// BackupVersion10 represents the original backup file format version.
	BackupVersion10 BackupVersion = "1.0"

	// BackupVersion20 represents the updated backup file format version which is using
	// restructured fields in order to be able to track custom storage volumes attached to the instance.
	BackupVersion20 BackupVersion = "2.0"
)
