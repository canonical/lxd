package drivers

import (
	"testing"
)

func TestPowerStoreVolumeResourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		given   Volume
		want    string
		wantErr bool
	}{
		{
			name: "container-volume",
			given: Volume{
				driver:      nil,
				pool:        "pool-name",
				volType:     VolumeTypeContainer,
				contentType: "",
				name:        "vol-name",
				config:      map[string]string{"volatile.uuid": "3a628d33-a689-462b-b23e-9a10e423b02e"},
				poolConfig:  map[string]string{},
			},
			want: "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-c_OmKNM6aJRiuyPpoQ5COwLg==",
		},
		{
			name: "vm-volume-iso",
			given: Volume{
				driver:      nil,
				pool:        "pool-name",
				volType:     VolumeTypeVM,
				contentType: ContentTypeISO,
				name:        "vol-name",
				config:      map[string]string{"volatile.uuid": "3a628d33-a689-462b-b23e-9a10e423b02e"},
				poolConfig:  map[string]string{},
			},
			want: "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-v_OmKNM6aJRiuyPpoQ5COwLg==.i",
		},
		{
			name: "vm-volume-block",
			given: Volume{
				driver:      nil,
				pool:        "pool-name",
				volType:     VolumeTypeVM,
				contentType: ContentTypeBlock,
				name:        "vol-name",
				config:      map[string]string{"volatile.uuid": "3a628d33-a689-462b-b23e-9a10e423b02e"},
				poolConfig:  map[string]string{},
			},
			want: "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-v_OmKNM6aJRiuyPpoQ5COwLg==.b",
		},
		{
			name: "image-volume",
			given: Volume{
				driver:      nil,
				pool:        "pool-name",
				volType:     VolumeTypeImage,
				contentType: "",
				name:        "vol-name",
				config:      map[string]string{"volatile.uuid": "3a628d33-a689-462b-b23e-9a10e423b02e"},
				poolConfig:  map[string]string{},
			},
			want: "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-i_OmKNM6aJRiuyPpoQ5COwLg==",
		},
		{
			name: "custom-volume",
			given: Volume{
				driver:      nil,
				pool:        "pool-name",
				volType:     VolumeTypeCustom,
				contentType: "",
				name:        "vol-name",
				config:      map[string]string{"volatile.uuid": "3a628d33-a689-462b-b23e-9a10e423b02e"},
				poolConfig:  map[string]string{},
			},
			want: "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-u_OmKNM6aJRiuyPpoQ5COwLg==",
		},
		{
			name: "other-name-and-uuid",
			given: Volume{
				driver:      nil,
				pool:        "other-pool-name",
				volType:     "unknown-vol-type",
				contentType: "unknown-vol-content-type",
				name:        "other-vol-name",
				config:      map[string]string{"volatile.uuid": "2731b28f-464b-4eac-b0cd-ec03d9effbf0"},
				poolConfig:  map[string]string{},
			},
			want: "lxd:omYUlOQRvuW2uffxWqsCQCue5SfEs4+khWLni1wNmC4=-JzGyj0ZLTqywzewD2e/78A==",
		},
		{
			name: "invalid-volume-volatile-uuid",
			given: Volume{
				driver:      nil,
				pool:        "pool-name",
				volType:     VolumeTypeCustom,
				contentType: "",
				name:        "vol-name",
				config:      map[string]string{"volatile.uuid": "invalid-value"},
				poolConfig:  map[string]string{},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := (&powerstore{common: common{name: test.given.pool}}).volumeResourceName(test.given)
			if err != nil && !test.wantErr {
				t.Fatalf("unexpected error while getting PowerStore volume name: %v", err)
			}

			if err == nil && test.wantErr {
				t.Fatalf("expected error while getting PowerStore volume name, got nil")
			}

			if test.want != got {
				t.Errorf("unexpected result of getting PowerStore volume name (want: %q, got %q)", test.want, got)
			}
		})
	}
}

func TestPowerStoreExtractDataFromVolumeResourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		given                 string
		wantPoolHash          string
		wantVolumeType        VolumeType
		wantVolumeUUID        string
		wantVolumeContentType ContentType
		wantErr               bool
	}{
		{
			name:                  "container-volume",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-c_OmKNM6aJRiuyPpoQ5COwLg==",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeContainer,
			wantVolumeUUID:        "3a628d33-a689-462b-b23e-9a10e423b02e",
			wantVolumeContentType: "",
		},
		{
			name:                  "vm-volume-iso",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-v_OmKNM6aJRiuyPpoQ5COwLg==.i",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeVM,
			wantVolumeUUID:        "3a628d33-a689-462b-b23e-9a10e423b02e",
			wantVolumeContentType: ContentTypeISO,
		},
		{
			name:                  "vm-volume-block",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-v_OmKNM6aJRiuyPpoQ5COwLg==.b",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeVM,
			wantVolumeUUID:        "3a628d33-a689-462b-b23e-9a10e423b02e",
			wantVolumeContentType: ContentTypeBlock,
		},
		{
			name:                  "image-volume",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-i_OmKNM6aJRiuyPpoQ5COwLg==",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeImage,
			wantVolumeUUID:        "3a628d33-a689-462b-b23e-9a10e423b02e",
			wantVolumeContentType: "",
		},
		{
			name:                  "custom-volume",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-u_OmKNM6aJRiuyPpoQ5COwLg==",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeCustom,
			wantVolumeUUID:        "3a628d33-a689-462b-b23e-9a10e423b02e",
			wantVolumeContentType: "",
		},
		{
			name:                  "missing-volume-type-and-content-type",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-OmKNM6aJRiuyPpoQ5COwLg==",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        "",
			wantVolumeUUID:        "3a628d33-a689-462b-b23e-9a10e423b02e",
			wantVolumeContentType: "",
		},
		{
			name:                  "missing-prefix",
			given:                 "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-u_OmKNM6aJRiuyPpoQ5COwLg==",
			wantPoolHash:          "",
			wantVolumeType:        "",
			wantVolumeUUID:        "00000000-0000-0000-0000-000000000000",
			wantVolumeContentType: "",
			wantErr:               true,
		},
		{
			name:                  "missing-pool-name-hash",
			given:                 "u_OmKNM6aJRiuyPpoQ5COwLg==",
			wantPoolHash:          "",
			wantVolumeType:        "",
			wantVolumeUUID:        "00000000-0000-0000-0000-000000000000",
			wantVolumeContentType: "",
			wantErr:               true,
		},
		{
			name:                  "missing-volume-data",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantPoolHash:          "",
			wantVolumeType:        "",
			wantVolumeUUID:        "00000000-0000-0000-0000-000000000000",
			wantVolumeContentType: "",
			wantErr:               true,
		},
		{
			name:                  "invalid-base64-encoded-volume-uuid",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-u_OmKNM6aJRiuyPpoQ5COwLg=",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeCustom,
			wantVolumeUUID:        "00000000-0000-0000-0000-000000000000",
			wantVolumeContentType: "",
			wantErr:               true,
		},
		{
			name:                  "invalid-volume-uuid",
			given:                 "lxd:2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=-u_OmKNM6aJRiuyPpoQ5COwLgaaaa==",
			wantPoolHash:          "2qwxIqsfgiGetqVdSVHgyhn3Kvtz65HGHeOkgAshhG8=",
			wantVolumeType:        VolumeTypeCustom,
			wantVolumeUUID:        "00000000-0000-0000-0000-000000000000",
			wantVolumeContentType: "",
			wantErr:               true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotPoolHash, gotVolumeType, gotVolumeUUID, gotVolumeContentType, err := (&powerstore{}).extractDataFromVolumeResourceName(test.given)
			if err != nil && !test.wantErr {
				t.Fatalf("unexpected error while retrieving data from PowerStore volume name: %v", err)
			}

			if err == nil && test.wantErr {
				t.Fatalf("expected error while retrieving data from PowerStore volume name, got nil")
			}

			if test.wantPoolHash != gotPoolHash {
				t.Errorf("wrong pool hash retrieved from PowerStore volume name (want: %q, got %q)", test.wantPoolHash, gotPoolHash)
			}

			if test.wantVolumeType != gotVolumeType {
				t.Errorf("wrong volume type retrieved from PowerStore volume name (want: %q, got %q)", test.wantVolumeType, gotVolumeType)
			}

			if test.wantVolumeUUID != gotVolumeUUID.String() {
				t.Errorf("wrong volume UUID retrieved from PowerStore volume name (want: %q, got %q)", test.wantVolumeUUID, gotVolumeUUID)
			}

			if test.wantVolumeContentType != gotVolumeContentType {
				t.Errorf("wrong volume content type retrieved from PowerStore volume name (want: %q, got %q)", test.wantVolumeContentType, gotVolumeContentType)
			}
		})
	}
}
