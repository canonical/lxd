package main

import (
	"reflect"
	"testing"

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
