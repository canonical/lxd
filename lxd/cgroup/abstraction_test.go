package cgroup

import (
	"testing"
)

func TestParseIOStatsV2(t *testing.T) {
	partMap := map[string]string{
		"8:0":   "sda",
		"8:16":  "sdb",
		"259:3": "nvme0n1",
	}

	tests := []struct {
		name    string
		ioStat  string
		want    map[string]IOStats
		wantErr bool
	}{
		{
			name:   "single device",
			ioStat: "259:3 rbytes=23227230208 wbytes=0 rios=466570 wios=0 dbytes=0 dios=0\n",
			want: map[string]IOStats{
				"nvme0n1": {ReadBytes: 23227230208, ReadsCompleted: 466570},
			},
		},
		{
			// Regression test for https://github.com/canonical/lxd/issues/18609:
			// a "(unknown)" device line must not abort parsing for the whole
			// instance. Captured verbatim from an affected container, including
			// the loop-device lines which carry no stats of interest.
			name: "unknown device is skipped, real devices still parsed",
			ioStat: "8:0 rbytes=44232704 wbytes=1759358976 rios=10799 wios=429531 dbytes=0 dios=0\n" +
				"7:1 rbytes=16384 wbytes=0 rios=3 wios=0 dbytes=0 dios=0\n" +
				"7:7 rbytes=2048 wbytes=0 rios=1 wios=0 dbytes=0 dios=0\n" +
				"(unknown) rbytes=0 wbytes=0 rios=39564 wios=0 dbytes=0 dios=0\n" +
				"259:3 rbytes=3387186688 wbytes=0 rios=39564 wios=0 dbytes=0 dios=0\n",
			want: map[string]IOStats{
				"sda":     {ReadBytes: 44232704, WrittenBytes: 1759358976, ReadsCompleted: 10799, WritesCompleted: 429531},
				"nvme0n1": {ReadBytes: 3387186688, ReadsCompleted: 39564},
			},
		},
		{
			// The kernel can emit several device IDs on one line; the last wins.
			name:   "multiple device IDs on one line",
			ioStat: "8:0 8:16 rbytes=352256 wbytes=0 rios=10 wios=0 dbytes=0 dios=0\n",
			want: map[string]IOStats{
				"sdb": {ReadBytes: 352256, ReadsCompleted: 10},
			},
		},
		{
			name:   "device absent from partMap is skipped",
			ioStat: "253:0 rbytes=1024 wbytes=0 rios=2 wios=0 dbytes=0 dios=0\n",
			want:   map[string]IOStats{},
		},
		{
			name:    "malformed stat value errors",
			ioStat:  "259:3 rbytes=notanumber wbytes=0\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIOStatsV2(tt.ioStat, partMap)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("got %d devices %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}

			for dev, wantStats := range tt.want {
				gotStats, ok := got[dev]
				if !ok {
					t.Errorf("missing device %q", dev)
					continue
				}

				if *gotStats != wantStats {
					t.Errorf("device %q = %+v, want %+v", dev, *gotStats, wantStats)
				}
			}
		})
	}
}
