package main

import (
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
