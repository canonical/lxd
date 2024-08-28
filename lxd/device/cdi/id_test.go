package cdi

import (
	"reflect"
	"testing"
)

func TestToVendor(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Vendor
		wantErr bool
	}{
		{"Valid Nvidia", "nvidia.com", NVIDIA, false},
		{"Invalid vendor", "amd.com", "", true},
		{"Empty string", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToVendor(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToVendor() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("ToVendor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToClass(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Class
		wantErr bool
	}{
		{"Valid GPU", "gpu", GPU, false},
		{"Valid IGPU", "igpu", IGPU, false},
		{"Valid MIG", "mig", MIG, false},
		{"Invalid class", "cpu", "", true},
		{"Empty string", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToClass(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToClass() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("ToClass() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIDEmpty(t *testing.T) {
	tests := []struct {
		name string
		id   ID
		want bool
	}{
		{"Empty ID", ID{}, true},
		{"Non-empty ID", ID{Vendor: NVIDIA, Class: GPU, Name: "0"}, false},
		{"Partial ID", ID{Vendor: NVIDIA}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.id.Empty()
			if got != tt.want {
				t.Errorf("ID.Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToCDI(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ID
		wantErr bool
	}{
		{"Valid GPU", "nvidia.com/gpu=0", ID{Vendor: NVIDIA, Class: GPU, Name: "0"}, false},
		{"Valid GPU all", "nvidia.com/gpu=all", ID{Vendor: NVIDIA, Class: GPU, Name: "all"}, false},
		{"Valid MIG", "nvidia.com/mig=0:1", ID{Vendor: NVIDIA, Class: MIG, Name: "0:1"}, false},
		{"Valid IGPU", "nvidia.com/igpu=0", ID{Vendor: NVIDIA, Class: IGPU, Name: "0"}, false},
		{"Valid GPU with UUID", "nvidia.com/gpu=GPU-8da9a1ee-3495-a369-a73a-b9d8ffbc1220", ID{Vendor: NVIDIA, Class: GPU, Name: "GPU-8da9a1ee-3495-a369-a73a-b9d8ffbc1220"}, false},
		{"Valid MIG with UUID", "nvidia.com/mig=MIG-8da9a1ee-3495-a369-a73a-b9d8ffbc1220", ID{Vendor: NVIDIA, Class: MIG, Name: "MIG-8da9a1ee-3495-a369-a73a-b9d8ffbc1220"}, false},
		{"Invalid vendor", "amd.com/gpu=0", ID{}, true},
		{"Invalid class", "nvidia.com/cpu=0", ID{}, true},
		{"Valid MIG format (all MIG indexes in device)", "nvidia.com/mig=0", ID{Vendor: NVIDIA, Class: MIG, Name: "0"}, false},
		{"Non-CDI format", "not-a-cdi-format", ID{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToCDI(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToCDI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ToCDI() = %v, want %v", got, tt.want)
			}
		})
	}
}
