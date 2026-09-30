package connectors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConnector implements only the methods that QualifiedNames uses.
type fakeConnector struct {
	Connector

	qn  string
	err error
}

func (f *fakeConnector) QualifiedName() (string, error) {
	return f.qn, f.err
}

// fakeMultiInitiatorConnector additionally implements MultiInitiatorConnector.
type fakeMultiInitiatorConnector struct {
	fakeConnector

	qns []string
	err error
}

func (f *fakeMultiInitiatorConnector) QualifiedNames() ([]string, error) {
	return f.qns, f.err
}

func Test_IsNVMe(t *testing.T) {
	tests := []struct {
		connectorType string
		want          bool
	}{
		{connectorType: TypeNVMeTCP, want: true},
		{connectorType: TypeNVMeFC, want: true},
		{connectorType: TypeISCSI, want: false},
		{connectorType: TypeSCSIFC, want: false},
		{connectorType: TypeSDC, want: false},
		{connectorType: TypeUnknown, want: false},
		{connectorType: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.connectorType, func(t *testing.T) {
			assert.Equal(t, test.want, IsNVMe(test.connectorType))
		})
	}
}

func Test_NewConnector(t *testing.T) {
	const serverUUID = "a5289556-c903-409a-8aa0-4af18a46738d"

	tests := []struct {
		connectorType string
		want          Connector
	}{
		{connectorType: TypeNVMeTCP, want: &connectorNVMe{common: common{serverUUID: serverUUID}}},
		{connectorType: TypeNVMeFC, want: &connectorNVMeFC{common: common{serverUUID: serverUUID}}},
		{connectorType: TypeSDC, want: &connectorSDC{common: common{serverUUID: serverUUID}}},
		{connectorType: TypeISCSI, want: &connectorISCSI{common: common{serverUUID: serverUUID}}},
		{connectorType: TypeSCSIFC, want: &connectorSCSIFC{common: common{serverUUID: serverUUID}}},
	}

	for _, test := range tests {
		t.Run(test.connectorType, func(t *testing.T) {
			connector, err := NewConnector(test.connectorType, serverUUID)
			require.NoError(t, err)
			assert.Equal(t, test.want, connector)
			assert.Equal(t, test.connectorType, connector.Type())
		})
	}

	t.Run("Unknown type", func(t *testing.T) {
		connector, err := NewConnector("unsupported", serverUUID)
		assert.EqualError(t, err, `Unknown storage connector type "unsupported"`)
		assert.Nil(t, connector)
	})

	t.Run("Unknown type constant", func(t *testing.T) {
		_, err := NewConnector(TypeUnknown, serverUUID)
		assert.Error(t, err)
	})
}

func Test_QualifiedNames(t *testing.T) {
	failure := errors.New("Failed getting qualified name")

	tests := []struct {
		name      string
		connector Connector
		want      []string
		wantError error
	}{
		{
			name:      "Single-initiator connector reports its one name",
			connector: &fakeConnector{qn: "iqn.2005-03.org.open-iscsi:abcdef123456"},
			want:      []string{"iqn.2005-03.org.open-iscsi:abcdef123456"},
		},
		{
			name:      "Single-initiator connector error is returned",
			connector: &fakeConnector{err: failure},
			wantError: failure,
		},
		{
			name: "Multi-initiator connector reports all of its names",
			connector: &fakeMultiInitiatorConnector{
				fakeConnector: fakeConnector{qn: "21000024ff43b10c"},
				qns:           []string{"21000024ff43b10c", "21000024ff43b10d"},
			},
			want: []string{"21000024ff43b10c", "21000024ff43b10d"},
		},
		{
			// The single name must not shadow the full set.
			name: "Multi-initiator connector does not fall back to the single name",
			connector: &fakeMultiInitiatorConnector{
				fakeConnector: fakeConnector{qn: "21000024ff43b10c"},
				err:           failure,
			},
			wantError: failure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			qns, err := QualifiedNames(test.connector)
			if test.wantError != nil {
				assert.ErrorIs(t, err, test.wantError)
				assert.Nil(t, qns)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, qns)
		})
	}

	t.Run("NVMe connector derives its name from the server UUID", func(t *testing.T) {
		connector, err := NewConnector(TypeNVMeTCP, "a5289556-c903-409a-8aa0-4af18a46738d")
		require.NoError(t, err)

		qns, err := QualifiedNames(connector)
		require.NoError(t, err)
		assert.Equal(t, []string{"nqn.2014-08.org.nvmexpress:uuid:a5289556-c903-409a-8aa0-4af18a46738d"}, qns)
	})
}
