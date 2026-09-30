package connectors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_nvmeFilterDiscoveryLog(t *testing.T) {
	tcpRecord := NVMeDiscoveryLogRecord{
		TransportType:    nvmeTransportTypeTCP,
		TransportAddress: "192.0.2.10",
		SubType:          SubtypeNVMESubsys,
		SubNQN:           "nqn.2010-06.com.example:array1",
	}

	fcRecord := NVMeDiscoveryLogRecord{
		TransportType:    nvmeTransportTypeFC,
		TransportAddress: "nn-0x20000024ff123456:pn-0x21000024ff123456",
		SubType:          SubtypeNVMESubsys,
		SubNQN:           "nqn.2010-06.com.example:array1",
	}

	discoveryRecord := NVMeDiscoveryLogRecord{
		TransportType:    nvmeTransportTypeTCP,
		TransportAddress: "192.0.2.10",
		SubType:          "discovery subsystem referral",
		SubNQN:           "nqn.2014-08.org.nvmexpress.discovery",
	}

	tests := []struct {
		name          string
		records       []NVMeDiscoveryLogRecord
		transportType string
		want          []NVMeDiscoveryLogRecord
	}{
		{
			name:          "Record of the requested transport is kept",
			records:       []NVMeDiscoveryLogRecord{tcpRecord},
			transportType: nvmeTransportTypeTCP,
			want:          []NVMeDiscoveryLogRecord{tcpRecord},
		},
		{
			name:          "Record of another transport is dropped",
			records:       []NVMeDiscoveryLogRecord{tcpRecord, fcRecord},
			transportType: nvmeTransportTypeFC,
			want:          []NVMeDiscoveryLogRecord{fcRecord},
		},
		{
			name:          "Record that is not an NVMe subsystem is dropped",
			records:       []NVMeDiscoveryLogRecord{discoveryRecord, tcpRecord},
			transportType: nvmeTransportTypeTCP,
			want:          []NVMeDiscoveryLogRecord{tcpRecord},
		},
		{
			name:          "Order of the kept records is preserved",
			records:       []NVMeDiscoveryLogRecord{fcRecord, tcpRecord, fcRecord},
			transportType: nvmeTransportTypeFC,
			want:          []NVMeDiscoveryLogRecord{fcRecord, fcRecord},
		},
		{
			name:          "No record matches",
			records:       []NVMeDiscoveryLogRecord{tcpRecord},
			transportType: nvmeTransportTypeFC,
			want:          []NVMeDiscoveryLogRecord{},
		},
		{
			name:          "No records at all",
			records:       nil,
			transportType: nvmeTransportTypeTCP,
			want:          []NVMeDiscoveryLogRecord{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, nvmeFilterDiscoveryLog(test.records, test.transportType))
		})
	}
}

func Test_nvmeNormalizeDiscoveryLog(t *testing.T) {
	tests := []struct {
		name    string
		records []NVMeDiscoveryLogRecord
		want    []NVMeDiscoveryLogRecord
	}{
		{
			name:    "TCP record without a port gets the default port",
			records: []NVMeDiscoveryLogRecord{{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.10"}},
			want: []NVMeDiscoveryLogRecord{
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.10", TransportServiceIdentifier: NVMeDefaultTransportPort},
			},
		},
		{
			name: "TCP record with a port keeps it",
			records: []NVMeDiscoveryLogRecord{
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.10", TransportServiceIdentifier: "4421"},
			},
			want: []NVMeDiscoveryLogRecord{
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.10", TransportServiceIdentifier: "4421"},
			},
		},
		{
			// Fibre Channel addresses have no port.
			name:    "FC record without a port is left alone",
			records: []NVMeDiscoveryLogRecord{{TransportType: nvmeTransportTypeFC, TransportAddress: "nn-0x20000024ff123456:pn-0x21000024ff123456"}},
			want:    []NVMeDiscoveryLogRecord{{TransportType: nvmeTransportTypeFC, TransportAddress: "nn-0x20000024ff123456:pn-0x21000024ff123456"}},
		},
		{
			name: "Records are normalized independently",
			records: []NVMeDiscoveryLogRecord{
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.10"},
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.11", TransportServiceIdentifier: "4421"},
			},
			want: []NVMeDiscoveryLogRecord{
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.10", TransportServiceIdentifier: NVMeDefaultTransportPort},
				{TransportType: nvmeTransportTypeTCP, TransportAddress: "192.0.2.11", TransportServiceIdentifier: "4421"},
			},
		},
		{
			name:    "No records at all",
			records: nil,
			want:    []NVMeDiscoveryLogRecord{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, nvmeNormalizeDiscoveryLog(test.records))
		})
	}
}

func Test_nvmeQualifiedName(t *testing.T) {
	assert.Equal(t, "nqn.2014-08.org.nvmexpress:uuid:a5289556-c903-409a-8aa0-4af18a46738d", nvmeQualifiedName("a5289556-c903-409a-8aa0-4af18a46738d"))
}
