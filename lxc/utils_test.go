package main

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared/api"
)

type utilsTestSuite struct {
	suite.Suite
}

func TestUtilsTestSuite(t *testing.T) {
	suite.Run(t, new(utilsTestSuite))
}

func (s *utilsTestSuite) TestIsAliasesSubsetTrue() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	s.True(IsAliasesSubset(a1, a2))
}

func (s *utilsTestSuite) TestIsAliasesSubsetFalse() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "baz"},
	}

	s.False(IsAliasesSubset(a1, a2))
}

func (s *utilsTestSuite) TestGetExistingAliases() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := GetExistingAliases([]string{"bar", "foo", "other"}, images)
	s.Exactly([]api.ImageAliasesEntry{images[0], images[1]}, aliases)
}

func (s *utilsTestSuite) TestGetExistingAliasesEmpty() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := GetExistingAliases([]string{"other1", "other2"}, images)
	s.Exactly([]api.ImageAliasesEntry{}, aliases)
}

func (s *utilsTestSuite) TestStructHasFields() {
	s.True(structHasField(reflect.TypeFor[api.Image](), "type"))
	s.True(structHasField(reflect.TypeFor[api.Image](), "public"))
	s.False(structHasField(reflect.TypeFor[api.Image](), "foo"))
}

func (s *utilsTestSuite) TestGetServerSupportedFilters() {
	filters := []string{
		"foo", "type=container", "user.blah=a", "status=running,stopped",
	}

	supportedFilters, unsupportedFilters := getServerSupportedFilters(filters, api.InstanceFull{})
	s.Equal([]string{"type=container"}, supportedFilters)
	s.Equal([]string{"foo", "user.blah=a", "status=running,stopped"}, unsupportedFilters)
}

func (s *utilsTestSuite) TestResolveRegistryImageSource() {
	confRemotes := map[string]config.Remote{
		"local": {
			Addr:    "https://127.0.0.1:8443",
			Project: "my-project",
		},
		"remote1": {
			Addr:     "https://images.example.com",
			Protocol: "simplestreams",
			Project:  "default",
		},
		"no-project": {
			Addr: "https://127.0.0.1:8443",
		},
	}

	tests := []struct {
		name            string
		imgRemote       string
		imgRef          string
		instRemote      string
		projectOverride string
		wantFingerprint string
		wantProject     string
		wantRegistry    string
	}{
		{
			name:            "Local image with project override",
			imgRemote:       "local",
			imgRef:          "abc123",
			instRemote:      "local",
			projectOverride: "custom-project",
			wantFingerprint: "abc123",
			wantProject:     "custom-project",
			wantRegistry:    "",
		},
		{
			name:            "Local image with remote project",
			imgRemote:       "local",
			imgRef:          "abc123",
			instRemote:      "local",
			projectOverride: "",
			wantFingerprint: "abc123",
			wantProject:     "my-project",
			wantRegistry:    "",
		},
		{
			name:            "Local image falls back to default project",
			imgRemote:       "no-project",
			imgRef:          "abc123",
			instRemote:      "no-project",
			projectOverride: "",
			wantFingerprint: "abc123",
			wantProject:     api.ProjectDefaultName,
			wantRegistry:    "",
		},
		{
			name:            "Empty image remote defaults to instance remote",
			imgRemote:       "",
			imgRef:          "abc123",
			instRemote:      "local",
			projectOverride: "",
			wantFingerprint: "abc123",
			wantProject:     "my-project",
			wantRegistry:    "",
		},
		{
			name:            "Remote image from image registry",
			imgRemote:       "remote1",
			imgRef:          "noble",
			instRemote:      "local",
			projectOverride: "",
			wantFingerprint: "noble",
			wantProject:     "",
			wantRegistry:    "remote1",
		},
		{
			name:            "Remote image from image registry ignores project override",
			imgRemote:       "remote1",
			imgRef:          "noble",
			instRemote:      "local",
			projectOverride: "custom-project",
			wantFingerprint: "noble",
			wantProject:     "",
			wantRegistry:    "remote1",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			imgInfo, registryName := resolveRegistryImageSource(confRemotes, tc.imgRemote, tc.imgRef, tc.instRemote, tc.projectOverride)

			s.Equal(tc.wantFingerprint, imgInfo.Fingerprint)
			s.Equal(tc.wantProject, imgInfo.Project)
			s.Equal(tc.wantRegistry, registryName)
		})
	}
}

func (s *utilsTestSuite) TestFormatTime() {
	// Create a fixed time for testing: 2026-08-10 12:30:45 UTC.
	fixedTime := time.Date(2026, 8, 10, 12, 30, 45, 0, time.UTC)

	// Create timezone locations.
	estLocation, err := time.LoadLocation("America/New_York")
	s.NoError(err)

	pstLocation, err := time.LoadLocation("America/Los_Angeles")
	s.NoError(err)

	// Create a zero time (Unix timestamp 0).
	zeroTime := time.Unix(0, 0)

	// Create an unset time (negative Unix timestamp).
	unsetTime := time.Unix(-1, 0)

	tests := []struct {
		name     string
		t        *time.Time
		location *time.Location
		want     string
	}{
		{
			name:     "Nil time pointer",
			t:        nil,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Zero time",
			t:        &zeroTime,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Unset time (negative Unix timestamp)",
			t:        &unsetTime,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Valid time in UTC with nil location defaults to UTC",
			t:        &fixedTime,
			location: nil,
			want:     "2026/08/10 12:30 UTC",
		},
		{
			name:     "Valid time in UTC",
			t:        &fixedTime,
			location: time.UTC,
			want:     "2026/08/10 12:30 UTC",
		},
		{
			name:     "Valid time in EST",
			t:        &fixedTime,
			location: estLocation,
			want:     "2026/08/10 08:30 EDT",
		},
		{
			name:     "Valid time in PST",
			t:        &fixedTime,
			location: pstLocation,
			want:     "2026/08/10 05:30 PDT",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := formatTime(tc.t, tc.location)
			s.Equal(tc.want, got)
		})
	}
}

func (s *utilsTestSuite) TestPrintTimeIfSet() {
	// Create a fixed time for testing: 2026-08-10 12:30:45 UTC.
	fixedTime := time.Date(2026, 8, 10, 12, 30, 45, 0, time.UTC)

	// Create a zero time (Unix timestamp 0).
	zeroTime := time.Unix(0, 0)

	// Create timezone locations.
	estLocation, err := time.LoadLocation("America/New_York")
	s.NoError(err)

	// Helper to create string pointers.
	strPtr := func(s string) *string {
		return &s
	}

	tests := []struct {
		name     string
		prefix   string
		t        *time.Time
		fallback *string
		location *time.Location
		want     string
	}{
		{
			name:     "Valid time with prefix",
			prefix:   "Created:",
			t:        &fixedTime,
			fallback: nil,
			location: time.UTC,
			want:     "Created: 2026/08/10 12:30 UTC\n",
		},
		{
			name:     "Valid time in EST timezone",
			prefix:   "Modified:",
			t:        &fixedTime,
			fallback: nil,
			location: estLocation,
			want:     "Modified: 2026/08/10 08:30 EDT\n",
		},
		{
			name:     "Nil time with no fallback returns empty",
			prefix:   "Created:",
			t:        nil,
			fallback: nil,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Zero time with no fallback returns empty",
			prefix:   "Created:",
			t:        &zeroTime,
			fallback: nil,
			location: time.UTC,
			want:     "",
		},
		{
			name:     "Nil time with fallback uses fallback",
			prefix:   "Created:",
			t:        nil,
			fallback: strPtr("MISSING"),
			location: time.UTC,
			want:     "Created: MISSING\n",
		},
		{
			name:     "Zero time with fallback uses fallback",
			prefix:   "Created:",
			t:        &zeroTime,
			fallback: strPtr("never"),
			location: time.UTC,
			want:     "Created: never\n",
		},
		{
			name:     "Valid time ignores fallback",
			prefix:   "Updated:",
			t:        &fixedTime,
			fallback: strPtr("FALLBACK_IGNORED"),
			location: time.UTC,
			want:     "Updated: 2026/08/10 12:30 UTC\n",
		},
		{
			name:     "Empty prefix with valid time",
			prefix:   "",
			t:        &fixedTime,
			fallback: nil,
			location: time.UTC,
			want:     "2026/08/10 12:30 UTC\n",
		},
		{
			name:     "Nil location defaults to UTC",
			prefix:   "Timestamp:",
			t:        &fixedTime,
			fallback: nil,
			location: nil,
			want:     "Timestamp: 2026/08/10 12:30 UTC\n",
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			buf := &bytes.Buffer{}
			printTimeIfSet(buf, tc.prefix, tc.t, tc.fallback, tc.location)
			s.Equal(tc.want, buf.String())
		})
	}
}
