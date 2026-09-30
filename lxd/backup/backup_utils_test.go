package backup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateBackupName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "backup0"},
		{name: "my-backup_1.2"},
		{name: "", wantErr: true},
		{name: ".", wantErr: true},
		{name: "..", wantErr: true},
		{name: "a..b", wantErr: true},
		{name: "a/b", wantErr: true},
		{name: "/abs", wantErr: true},
		{name: `a\b`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateBackupName(tt.name)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, got)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.name, got)
		})
	}
}
