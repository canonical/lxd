package drivers

import (
	"fmt"
	"testing"
)

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

			got := d.getRBDVolumeName(tt.args.vol, tt.args.snapName, tt.args.zombie, tt.args.withPoolName)
			if got != tt.want {
				t.Errorf("ceph.getRBDVolumeName() = %v, want %v", got, tt.want)
			}
		})
	}
}
func Example_ceph_parseParent() {
	d := &ceph{}

	parents := []string{
		"pool/zombie_image_9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb_ext4.block@readonly",
		"pool/zombie_image_9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb_ext4.block",
		"pool/image_9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb_ext4.block@readonly",
		"pool/zombie_image_9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb_ext4@readonly",
		"pool/zombie_image_9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb_ext4",
		"pool/image_9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb_ext4@readonly",
		"pool/zombie_image_2cfc5a5567b8d74c0986f3d8a77a2a78e58fe22ea9abd2693112031f85afa1a1_xfs@zombie_snapshot_7f6d679b-ee25-419e-af49-bb805cb32088",
		"pool/container_bar@zombie_snapshot_ce77e971-6c1b-45c0-b193-dba9ec5e7d82",
		"pool/container_test-project_c4.block",
		"pool/zombie_container_test-project_c1_28e7a7ab-740a-490c-8118-7caf7810f83b@zombie_snapshot_1027f4ab-de11-4cee-8015-bd532a1fed76",
	}

	for _, parent := range parents {
		vol, snapName, err := d.parseParent(parent)
		fmt.Println(vol.pool, vol.volType, vol.name, vol.config["block.filesystem"], vol.contentType, snapName, err)
	}

	// Output: pool zombie_image 9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb ext4 block readonly <nil>
	// pool zombie_image 9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb ext4 block  <nil>
	// pool image 9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb ext4 block readonly <nil>
	// pool zombie_image 9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb ext4 filesystem readonly <nil>
	// pool zombie_image 9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb ext4 filesystem  <nil>
	// pool image 9e90b7b9ccdd7a671a987fadcf07ab92363be57e7f056d18d42af452cdaf95bb ext4 filesystem readonly <nil>
	// pool zombie_image 2cfc5a5567b8d74c0986f3d8a77a2a78e58fe22ea9abd2693112031f85afa1a1 xfs filesystem zombie_snapshot_7f6d679b-ee25-419e-af49-bb805cb32088 <nil>
	// pool container bar  filesystem zombie_snapshot_ce77e971-6c1b-45c0-b193-dba9ec5e7d82 <nil>
	// pool container test-project_c4  block  <nil>
	// pool zombie_container test-project_c1_28e7a7ab-740a-490c-8118-7caf7810f83b  filesystem zombie_snapshot_1027f4ab-de11-4cee-8015-bd532a1fed76 <nil>
}

func Test_ceph_findLastCommonSnapshot(t *testing.T) {
	tests := []struct {
		name             string
		targetSnapshots  []Volume
		refreshSnapshots []string
		want             int
	}{
		{
			"Volume without target snapshots",
			[]Volume{},
			[]string{},
			-1,
		},
		{
			"Volume without target snapshots that requires refresh (no valid use case, just to test)",
			[]Volume{},
			[]string{"snap0"},
			-1,
		},
		{
			"Volume that requires refreshing everything",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
			},
			[]string{"snap0", "snap1", "snap2"},
			-1,
		},
		{
			"Volume with the first snapshot requiring refresh",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
			},
			[]string{"snap0"},
			-1,
		},
		{
			"Volume with target snapshots and nothing to refresh",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
			},
			[]string{},
			2,
		},
		{
			"Volume with target snapshots and the last one to refresh",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
			},
			[]string{"snap2"},
			1,
		},
		{
			"Volume with target snapshots and the last two to refresh",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
				{
					name: "v1/snap3",
				},
				{
					name: "v1/snap4",
				},
			},
			[]string{"snap3", "snap4"},
			2,
		},
		{
			"Volume with target snapshots and an out of order sync",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
			},
			[]string{"snap1"},
			0,
		},
		{
			"Volume with target snapshots and multiple out of order syncs",
			[]Volume{
				{
					name: "v1/snap0",
				},
				{
					name: "v1/snap1",
				},
				{
					name: "v1/snap2",
				},
				{
					name: "v1/snap3",
				},
				{
					name: "v1/snap4",
				},
			},
			[]string{"snap1", "snap3"},
			0,
		},
	}

	d := &ceph{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.findLastCommonSnapshotIndex(tt.targetSnapshots, tt.refreshSnapshots)
			if got != tt.want {
				t.Errorf("Found wrong last common snapshot: %d != %d", got, tt.want)
			}
		})
	}
}
