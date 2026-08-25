package drivers

import (
	"testing"
)

func Test_ceph_Validate_replicatorPoolKey(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]string
		wantErr bool
	}{
		{"Replicator key naming a peer site", map[string]string{"ceph.replicator.dr": "site-b"}, false},
		{"Replicator key of a project whose name holds a full stop", map[string]string{"ceph.replicator.dr.eu": "site-b"}, false},
		{"Replicator key unset by an empty peer site", map[string]string{"ceph.replicator.dr": ""}, false},
		{"Replicator key without a project name", map[string]string{"ceph.replicator.": "site-b"}, true},
		{"Unknown ceph key", map[string]string{"ceph.replicated.dr": "site-b"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &ceph{}
			d.init(nil, "testpool", tt.config, nil, nil, &Validators{
				PoolRules:   func() map[string]func(string) error { return map[string]func(string) error{} },
				VolumeRules: func(_ Volume) map[string]func(string) error { return map[string]func(string) error{} },
			})

			err := d.Validate(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unexpected validation result: %v", err)
			}
		})
	}
}
