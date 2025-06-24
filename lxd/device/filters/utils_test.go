package filters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilter(t *testing.T) {
	tests := []struct {
		name   string
		filter Filter
		device map[string]string
		result bool
	}{
		{
			name:   "IsDisk returns true if type is disk",
			filter: IsDisk,
			device: map[string]string{
				"type": "disk",
			},
			result: true,
		},
		{
			name:   "IsDisk returns false if type is not disk",
			filter: IsDisk,
			device: map[string]string{
				"type": "foo",
			},
			result: false,
		},
		{
			name:   "Not(IsDisk) returns true if type is not disk",
			filter: Not(IsDisk),
			device: map[string]string{
				"type": "foo",
			},
			result: true,
		},
		{
			name:   "Not(IsDisk) returns false if type is disk",
			filter: Not(IsDisk),
			device: map[string]string{
				"type": "disk",
			},
			result: false,
		},
		{
			name:   "Or(IsDisk, IsNIC) returns true if type is disk",
			filter: Or(IsDisk, IsNIC),
			device: map[string]string{
				"type": "disk",
			},
			result: true,
		},
		{
			name:   "Or(IsDisk, IsNIC) returns true if type is nic",
			filter: Or(IsDisk, IsNIC),
			device: map[string]string{
				"type": "nic",
			},
			result: true,
		},
		{
			name:   "Or(IsDisk, IsNIC) returns false if type neither disk nor nic",
			filter: Or(IsDisk, IsNIC),
			device: map[string]string{
				"type": "foo",
			},
			result: false,
		},
		{
			name:   "Not(IsDisk) returns true if type is nic",
			filter: Not(IsDisk),
			device: map[string]string{
				"type": "nic",
			},
			result: true,
		},
		{
			name:   "Not(Or(IsDisk, IsNIC)) returns true if type is neither disk nor nic",
			filter: Not(Or(IsDisk, IsNIC)),
			device: map[string]string{
				"type": "foo",
			},
			result: true,
		},
	}

	for _, test := range tests {
		result := test.filter(test.device)
		assert.Equal(t, test.result, result, test.name)
	}
}
