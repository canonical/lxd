package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/canonical/lxd/shared/api"
)

type utilsTestSuite struct {
	suite.Suite
}

// Runs a test suite for utility functions, using the provided testing.T instance.
func TestUtilsTestSuite(t *testing.T) {
	suite.Run(t, new(utilsTestSuite))
}

// Checks if one array of image aliases is a subset of another, expecting the result to be true.
func (s *utilsTestSuite) TestIsAliasesSubsetTrue() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	s.Exactly(IsAliasesSubset(a1, a2), true)
}

// Checks if one array of image aliases is a subset of another, expecting the result to be false.
func (s *utilsTestSuite) TestIsAliasesSubsetFalse() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "baz"},
	}

	s.Exactly(IsAliasesSubset(a1, a2), false)
}

// Retrieves the retrieval of existing image aliases based on a list of provided aliases.
func (s *utilsTestSuite) TestGetExistingAliases() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := GetExistingAliases([]string{"bar", "foo", "other"}, images)
	s.Exactly([]api.ImageAliasesEntry{images[0], images[1]}, aliases)
}

// Retrieves the retrieval of existing image aliases when no matches are found.
func (s *utilsTestSuite) TestGetExistingAliasesEmpty() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := GetExistingAliases([]string{"other1", "other2"}, images)
	s.Exactly([]api.ImageAliasesEntry{}, aliases)
}

// Determines the presence of specific fields in a given struct type, expecting true if the fields exist and false if they don't.
func (s *utilsTestSuite) TestStructHasFields() {
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "type"), true)
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "public"), true)
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "foo"), false)
}

// Filters the filtering of supported and unsupported filters based on a provided list of filters and an instance of the API structure.
func (s *utilsTestSuite) TestGetServerSupportedFilters() {
	filters := []string{
		"foo", "type=container", "user.blah=a", "status=running,stopped",
	}

	supportedFilters, unsupportedFilters := getServerSupportedFilters(filters, api.InstanceFull{})
	s.Equal([]string{"type=container"}, supportedFilters)
	s.Equal([]string{"foo", "user.blah=a", "status=running,stopped"}, unsupportedFilters)
}
