package ubuntupro

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/canonical/lxd/shared/version"
)

type proCLIMock struct {
	mockErr      error
	mockAttached bool
}

func (p proCLIMock) isHostAttached() (bool, error) {
	return p.mockAttached, p.mockErr
}

func TestClient(t *testing.T) {

	t.Run("UserAgent does not add pro feature when unsupported OS", func(t *testing.T) {
		_ = New("Debian")
		assert.NotContains(t, version.UserAgent, "pro")
	})

	t.Run("UserAgent does not add pro feature when not attached", func(t *testing.T) {
		mockProCLI := proCLIMock{
			mockAttached: false,
		}

		s := &Client{}
		s.init(mockProCLI)

		assert.NotContains(t, version.UserAgent, "pro")
	})

	t.Run("UserAgent does not add pro feature when error checking status", func(t *testing.T) {
		mockProCLI := proCLIMock{
			mockErr: errors.New("Foo"),
		}

		s := &Client{}
		s.init(mockProCLI)

		assert.NotContains(t, version.UserAgent, "pro")
	})

	t.Run("UserAgent adds pro feature when attached", func(t *testing.T) {
		mockProCLI := proCLIMock{
			mockAttached: true,
		}

		s := &Client{}
		s.init(mockProCLI)

		assert.Contains(t, version.UserAgent, "pro")
	})
}
