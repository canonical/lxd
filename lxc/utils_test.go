package main

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/suite"

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
	sort.Sort(stringList(data))
	s.Equal([][]string{{"baz", "bza"}, {"foo", "bar"}}, data)
}

// The first different string is used in sorting.
func (s *utilsTestSuite) Test_stringList_sort_by_column() {
	data := [][]string{{"foo", "baz"}, {"foo", "bar"}}
	sort.Sort(stringList(data))
	s.Equal([][]string{{"foo", "bar"}, {"foo", "baz"}}, data)
}

// Empty strings are sorted last.
func (s *utilsTestSuite) Test_stringList_empty_strings() {
	data := [][]string{{"", "bar"}, {"foo", "baz"}}
	sort.Sort(stringList(data))
	s.Equal([][]string{{"foo", "baz"}, {"", "bar"}}, data)
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
