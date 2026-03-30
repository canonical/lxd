package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReplicatorIsScheduledNow(t *testing.T) {
	// Use a fixed reference time to avoid flakiness around minute boundaries.
	defaultNow := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	// A spec that fires at defaultNow (10:30) should match.
	firesNow := fmt.Sprintf("%d %d * * *", defaultNow.Minute(), defaultNow.Hour())

	// A spec that fires one minute after defaultNow (10:31) should not match.
	firesLater := fmt.Sprintf("%d %d * * *", defaultNow.Add(time.Minute).Minute(), defaultNow.Add(time.Minute).Hour())

	tests := []struct {
		name string
		spec string
		now  time.Time // zero means use defaultNow
		want bool
	}{
		// Invalid / empty specs.
		{name: "empty spec", spec: "", want: false},
		{name: "invalid spec", spec: "not-a-cron", want: false},
		{name: "too many fields", spec: "1 2 3 4 5 6 7", want: false},

		// Standard aliases; do not fire at the fixed reference time.
		{name: "@yearly alias", spec: "@yearly", want: false},
		{name: "@monthly alias", spec: "@monthly", want: false},
		{name: "@weekly alias", spec: "@weekly", want: false},

		// Descriptor aliases that fire exactly at the scheduled minute.
		{name: "@hourly fires at the full hour", spec: "@hourly", now: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC), want: true},
		{name: "@daily fires at midnight", spec: "@daily", now: time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC), want: true},

		// Dynamically constructed; fires exactly now.
		{name: "fires now", spec: firesNow, want: true},

		// Comma-separated: one spec fires now, other fires later.
		{name: "comma-separated fires now", spec: firesNow + ", " + firesLater, want: true},

		// Comma-separated: neither spec fires now.
		{name: "comma-separated both later", spec: firesLater + ", " + firesLater, want: false},

		// Intra-field comma list: fires at minutes 30 and 31; defaultNow is minute 30.
		{name: "intra-field comma list fires now", spec: fmt.Sprintf("%d,%d %d * * *", defaultNow.Minute(), defaultNow.Add(time.Minute).Minute(), defaultNow.Hour()), want: true},

		// Dynamically constructed; fires one minute from now.
		{name: "fires one minute later", spec: firesLater, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := tt.now
			if now.IsZero() {
				now = defaultNow
			}

			got := replicatorIsScheduledNow(tt.spec, now)
			assert.Equal(t, tt.want, got)
		})
	}
}
