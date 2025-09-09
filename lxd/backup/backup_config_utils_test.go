package backup

import (
	"testing"
	"time"

	"github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/shared/api"
)

func TestConvertFormat(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name               string
		sourceBackupConf   *config.Config
		expectedBackupConf *config.Config
		convertToVersion   uint32
	}{
		{
			name:               "The conversion doesn't drop the internal metadata (to 2)",
			sourceBackupConf:   config.NewConfig(now),
			expectedBackupConf: config.NewConfig(now),
			convertToVersion:   api.BackupMetadataVersion2,
		},
		{
			name:               "The conversion doesn't drop the internal metadata (to 1)",
			sourceBackupConf:   config.NewConfig(now),
			expectedBackupConf: config.NewConfig(now),
			convertToVersion:   api.BackupMetadataVersion1,
		},
	}

	for _, test := range tests {
		convertedBackupConf, err := ConvertFormat(test.sourceBackupConf, test.convertToVersion)
		if err != nil {
			t.Errorf("%s: Failed converting the format: %v", test.name, err)
		}

		if !convertedBackupConf.LastModified().Equal(test.expectedBackupConf.LastModified()) {
			t.Errorf("%s: Last modified times don't match: %q != %q", test.name, convertedBackupConf.LastModified(), test.expectedBackupConf.LastModified())
		}
	}
}
