package drivers

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/canonical/lxd/shared"
)

func Test_ceph_cephMirrorExitStatus(t *testing.T) {
	// Only a command that ran yields an exit error, so one exits the way rbd does when librbd
	// returns EBUSY.
	rbdErr := shared.NewRunError("rbd", []string{"mirror", "image", "promote"}, exec.Command("sh", "-c", "exit 16").Run(), &bytes.Buffer{}, &bytes.Buffer{})

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"Failed rbd command", rbdErr, 16},
		{"Error wrapped by the caller", fmt.Errorf("Failed promoting volume: %w", rbdErr), 16},
		// Only the status a command exited with counts, not an error that merely quotes one.
		{"Error that did not come from a command", errors.New("exit status 16"), -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cephMirrorExitStatus(tt.err)
			if got != tt.want {
				t.Errorf("Unexpected exit status: %d != %d", got, tt.want)
			}
		})
	}
}

func Test_ceph_cephMirrorErrorSays(t *testing.T) {
	// rbd prints the librbd log line that names the refusal, then its own summary.
	rbdError := func(stderr string) error {
		return shared.NewRunError("rbd", []string{"mirror", "image"}, errors.New("exit status 16"), &bytes.Buffer{}, bytes.NewBufferString(stderr))
	}

	peerPrimary := rbdError("2026-09-28T08:20:09.796+0000 7fecc59a36c0 -1 librbd::mirror::PromoteRequest: 0x7feca8030dc0 handle_get_info: image is primary within a remote cluster or demotion is not propagated yet\nrbd: error promoting image to primary\n")
	alreadyPrimary := rbdError("2026-09-28T08:20:09.796+0000 7fecc59a36c0 -1 librbd::mirror::PromoteRequest: 0x7feca8030dc0 handle_get_info: image is already primary\nrbd: error promoting image to primary\n")

	tests := []struct {
		name    string
		err     error
		message string
		want    bool
	}{
		{"Promotion of an image that is already primary", alreadyPrimary, "already primary", true},
		// A refusal read as an image that is already primary would be tolerated instead of reported.
		{"Refusal is not an image that is already primary", peerPrimary, "already primary", false},
		{"Error wrapped by the caller", fmt.Errorf("Failed promoting volume: %w", alreadyPrimary), "already primary", true},
		// Only what rbd printed counts, not an error that merely quotes the message.
		{"Error that did not come from rbd", errors.New("image is already primary"), "already primary", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cephMirrorErrorSays(tt.err, tt.message)
			if got != tt.want {
				t.Errorf("Matched %q wrongly: %t != %t", tt.message, got, tt.want)
			}
		})
	}
}
