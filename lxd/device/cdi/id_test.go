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
		{"Valid AMD", "amd.com", AMD, false},
		{"Invalid vendor", "unknown.com", "", true},
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

func TestToCDI(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *ID
		wantErr bool
	}{
		{"Valid NVIDIA GPU", "nvidia.com/gpu=0", &ID{Vendor: NVIDIA, Class: GPU, Name: "0"}, false},
		{"Valid NVIDIA GPU all", "nvidia.com/gpu=all", &ID{Vendor: NVIDIA, Class: GPU, Name: "all"}, false},
		{"Valid NVIDIA MIG", "nvidia.com/mig=0:1", &ID{Vendor: NVIDIA, Class: MIG, Name: "0:1"}, false},
		{"Valid NVIDIA IGPU", "nvidia.com/igpu=0", &ID{Vendor: NVIDIA, Class: IGPU, Name: "0"}, false},
		{"Valid NVIDIA GPU with UUID", "nvidia.com/gpu=GPU-8da9a1ee-3495-a369-a73a-b9d8ffbc1220", &ID{Vendor: NVIDIA, Class: GPU, Name: "GPU-8da9a1ee-3495-a369-a73a-b9d8ffbc1220"}, false},
		{"Valid NVIDIA MIG with UUID", "nvidia.com/mig=MIG-8da9a1ee-3495-a369-a73a-b9d8ffbc1220", &ID{Vendor: NVIDIA, Class: MIG, Name: "MIG-8da9a1ee-3495-a369-a73a-b9d8ffbc1220"}, false},
		{"Valid AMD GPU", "amd.com/gpu=0", &ID{Vendor: AMD, Class: GPU, Name: "0"}, false},
		{"Valid AMD GPU all", "amd.com/gpu=all", &ID{Vendor: AMD, Class: GPU, Name: "all"}, false},
		{"Valid AMD IGPU", "amd.com/igpu=0", &ID{Vendor: AMD, Class: IGPU, Name: "0"}, false},
		{"Invalid vendor", "unknown.com/gpu=0", nil, true},
		{"Invalid class", "nvidia.com/cpu=0", nil, true},
		{"Valid MIG format (all MIG indexes in device)", "nvidia.com/mig=0", &ID{Vendor: NVIDIA, Class: MIG, Name: "0"}, false},
		{"Non-CDI format", "not-a-cdi-format", nil, true},
		{"DRM ID", "1", nil, true},
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
