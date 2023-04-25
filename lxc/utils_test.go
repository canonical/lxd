package main

import (
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared/api"
)

type utilsTestSuite struct {
	suite.Suite
}

func TestUtilsTestSuite(t *testing.T) {
	suite.Run(t, new(utilsTestSuite))
}

// stringList can be used to sort a list of strings.
func (s *utilsTestSuite) Test_stringList() {
	data := [][]string{{"foo", "bar"}, {"baz", "bza"}}
	sort.Sort(utils.StringList(data))
	s.Equal([][]string{{"baz", "bza"}, {"foo", "bar"}}, data)
}

// The first different string is used in sorting.
func (s *utilsTestSuite) Test_stringList_sort_by_column() {
	data := [][]string{{"foo", "baz"}, {"foo", "bar"}}
	sort.Sort(utils.StringList(data))
	s.Equal([][]string{{"foo", "bar"}, {"foo", "baz"}}, data)
}

// Empty strings are sorted last.
func (s *utilsTestSuite) Test_stringList_empty_strings() {
	data := [][]string{{"", "bar"}, {"foo", "baz"}}
	sort.Sort(utils.StringList(data))
	s.Equal([][]string{{"foo", "baz"}, {"", "bar"}}, data)
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

	s.Exactly(IsAliasesSubset(a1, a2), true)
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

	s.Exactly(IsAliasesSubset(a1, a2), false)
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
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "type"), true)
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "public"), true)
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "foo"), false)
}

func (s *utilsTestSuite) TestGetServerSupportedFilters() {
	filters := []string{
		"foo", "type=container", "user.blah=a", "status=running,stopped",
	}

	supportedFilters, unsupportedFilters := getServerSupportedFilters(filters, api.InstanceFull{})
	s.Equal([]string{"type=container"}, supportedFilters)
	s.Equal([]string{"foo", "user.blah=a", "status=running,stopped"}, unsupportedFilters)
}
