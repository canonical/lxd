package main

import (
	"testing"

	"github.com/lxc/lxd/shared"
)

func Test_config_value_set_empty_removes_val(t *testing.T) {
	d := &Daemon{}

	err := shared.SetLogger("", "", true, true)
	if err != nil {
		t.Error("logging")
	}

	err = initializeDbObject(d, ":memory:")
	defer d.db.Close()

	if err != nil {
		t.Error("failed to init db")
	}
	if err = d.ConfigValueSet("core.lvm_vg_name", "foo"); err != nil {
		t.Error("couldn't set value", err)
	}

	val, err := d.ConfigValueGet("core.lvm_vg_name")
	if err != nil {
		t.Error("Error getting val")
	}
	if val != "foo" {
		t.Error("Expected foo, got ", val)
	}

	err = d.ConfigValueSet("core.lvm_vg_name", "")
	if err != nil {
		t.Error("error setting to ''")
	}

	val, err = d.ConfigValueGet("core.lvm_vg_name")
	if err != nil {
		t.Error("Error getting val")
	}
	if val != "" {
		t.Error("Expected '', got ", val)
	}

	valMap, err := d.ConfigValuesGet()
	if err != nil {
		t.Error("Error getting val")
	}
	if key, present := valMap["core.lvm_vg_name"]; present {
		t.Errorf("un-set key should not be in values map, it is '%v'", key)
	}

}
