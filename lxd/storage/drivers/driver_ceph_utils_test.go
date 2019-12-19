package drivers

import "testing"

func Test_ceph_getRBDVolumeName(t *testing.T) {
	type args struct {
		vol          Volume
		snapName     string
		zombie       bool
		withPoolName bool
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			"Volume without pool name",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol", nil, nil),
				snapName:     "",
				zombie:       false,
				withPoolName: false,
			},
			"container_testvol",
		},
		{
			"Volume with unknown type and without pool name",
			args{
				vol:          NewVolume(nil, "testpool", VolumeType("unknown"), ContentTypeFS, "testvol", nil, nil),
				snapName:     "",
				zombie:       false,
				withPoolName: false,
			},
			"unknown_testvol",
		},
		{
			"Volume without pool name in zombie mode",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol", nil, nil),
				snapName:     "",
				zombie:       true,
				withPoolName: false,
			},
			"zombie_container_testvol",
		},
		{
			"Volume with pool name in zombie mode",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol", nil, nil),
				snapName:     "",
				zombie:       true,
				withPoolName: true,
			},
			"testosdpool/zombie_container_testvol",
		},
		{
			"Volume snapshot with dedicated snapshot name and without pool name",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol", nil, nil),
				snapName:     "snapshot_testsnap",
				zombie:       false,
				withPoolName: false,
			},
			"container_testvol@snapshot_testsnap",
		},
		{
			"Volume snapshot with dedicated snapshot name and pool name",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol", nil, nil),
				snapName:     "snapshot_testsnap",
				zombie:       false,
				withPoolName: true,
			},
			"testosdpool/container_testvol@snapshot_testsnap",
		},
		{
			"Volume snapshot with pool name",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol/testsnap", nil, nil),
				snapName:     "",
				zombie:       false,
				withPoolName: true,
			},
			"testosdpool/container_testvol@snapshot_testsnap",
		},
		{
			"Volume snapshot with additional dedicated snapshot name and pool name",
			args{
				vol:          NewVolume(nil, "testpool", VolumeTypeContainer, ContentTypeFS, "testvol/testsnap", nil, nil),
				snapName:     "testsnap1",
				zombie:       false,
				withPoolName: true,
			},
			"testosdpool/container_testvol@testsnap1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &ceph{
				common{
					config: map[string]string{
						"ceph.osd.pool_name": "testosdpool",
					},
				},
			}
			if got := d.getRBDVolumeName(tt.args.vol, tt.args.snapName, tt.args.zombie, tt.args.withPoolName); got != tt.want {
				t.Errorf("ceph.getRBDVolumeName() = %v, want %v", got, tt.want)
			}
		})
	}
}
