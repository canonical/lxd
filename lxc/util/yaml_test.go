package printers

import (
	"bytes"
	"testing"

	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/assert"
)

func TestYAMLPrinter(t *testing.T) {

	tests := []struct {
		name     string
		data     any
		expected string
		wantErr  bool
	}{
		{
			name:     "yaml format no data",
			data:     nil,
			wantErr:  false,
			expected: "",
		},
		{
			name: "yaml array format",
			data: api.Cluster{
				ServerName: "server",
				Enabled:    false,
				MemberConfig: []api.ClusterMemberConfigKey{
					{
						Entity:      "entity1",
						Name:        "node1",
						Key:         "12345",
						Value:       "value",
						Description: "d1",
					},
					{
						Entity:      "entity2",
						Name:        "node2",
						Key:         "54321",
						Value:       "value2",
						Description: "d2",
					},
				},
			},
			wantErr: false,
			expected: `server_name: server
enabled: false
member_config:
- entity: entity1
  name: node1
  key: "12345"
  value: value
  description: d1
- entity: entity2
  name: node2
  key: "54321"
  value: value2
  description: d2
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := bytes.NewBuffer([]byte{})
			printer := NewYAMLPrinter()
			err := printer.PrintObj(test.data, out)
			if test.wantErr && err != nil {
				t.Errorf("Run() error = %v, wantErr %v", err, test.wantErr)
			}
			assert.Equal(t, test.expected, out.String())
		})
	}

}
