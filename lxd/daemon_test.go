package main

func (suite *lxdTestSuite) Test_config_value_set_empty_removes_val() {
	var err error
	d := suite.d

	err = d.ConfigValueSet("storage.lvm_vg_name", "foo")
	suite.Req.Nil(err)

	val, err := d.ConfigValueGet("storage.lvm_vg_name")
	suite.Req.Nil(err)
	suite.Req.Equal(val, "foo")

	err = d.ConfigValueSet("storage.lvm_vg_name", "")
	suite.Req.Nil(err)

	val, err = d.ConfigValueGet("storage.lvm_vg_name")
	suite.Req.Nil(err)
	suite.Req.Equal(val, "")

	valMap, err := d.ConfigValuesGet()
	suite.Req.Nil(err)

	_, present := valMap["storage.lvm_vg_name"]
	suite.Req.False(present)
}
