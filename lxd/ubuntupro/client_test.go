package ubuntupro

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestParseProAPIIsAttachedV1(t *testing.T) {
	t.Run("Valid attached", func(t *testing.T) {
		response := `{"data": {"attributes": {"is_attached_and_contract_valid": true}}}`
		attached, err := parseProAPIIsAttachedV1(response)
		require.NoError(t, err)
		assert.True(t, attached)
	})

	t.Run("Valid detached", func(t *testing.T) {
		response := `{"data": {"attributes": {"is_attached_and_contract_valid": false}}}`
		attached, err := parseProAPIIsAttachedV1(response)
		require.NoError(t, err)
		assert.False(t, attached)
	})

	t.Run("Missing data", func(t *testing.T) {
		response := `{"foo": "bar"}`
		_, err := parseProAPIIsAttachedV1(response)
		require.Error(t, err)
		assert.Equal(t, "Received unexpected response from Ubuntu Pro client: missing attached field", err.Error())
	})

	t.Run("Missing attributes", func(t *testing.T) {
		response := `{"data": {"foo": "bar"}}`
		_, err := parseProAPIIsAttachedV1(response)
		require.Error(t, err)
		assert.Equal(t, "Received unexpected response from Ubuntu Pro client: missing attached field", err.Error())
	})

	t.Run("Missing attached field", func(t *testing.T) {
		response := `{"data": {"attributes": {"foo": "bar"}}}`
		_, err := parseProAPIIsAttachedV1(response)
		require.Error(t, err)
		assert.Equal(t, "Received unexpected response from Ubuntu Pro client: missing attached field", err.Error())
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		response := `invalid`
		_, err := parseProAPIIsAttachedV1(response)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid character")
	})
}
