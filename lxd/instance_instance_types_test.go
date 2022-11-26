package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceTypesRefresh(t *testing.T) {

	err := instanceRefreshTypes(context.TODO(), &Daemon{}, "", "")
	if err == nil {
		t.Fatal(err)
	}
	dir, rollback := preperaInstanceTypesFile(t)
	defer rollback()

	err = instanceRefreshTypes(context.TODO(), &Daemon{}, dir, "")
	if err != nil {
		t.Fatal(err)
	}

}

func preperaInstanceTypesFile(t *testing.T) (string, func()) {
	dir, err := os.MkdirTemp("/tmp", "lxd_*")
	if err != nil {
		t.Fatal(err)
	}

	flavorsContent := `
aws:
  c1.medium:
    cpu: 2.0
    mem: 1.7
  c1.xlarge:
    cpu: 8.0
    mem: 15.0 
`
	err = os.WriteFile(filepath.Join(dir, "01_instance_types.yaml"), []byte(flavorsContent), 0600)
	if err != nil {
		t.Fatal(err)
	}
	flavorsContent = `
gce:
  c1.medium:
    cpu: 2.0
    mem: 1.7
  c1.xlarge:
    cpu: 8.0
    mem: 15.0 
aws:
  c1.medium:
    cpu: 2.0
    mem: 2.0
`
	err = os.WriteFile(filepath.Join(dir, "02_instance_types.yaml"), []byte(flavorsContent), 0600)
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() {
		os.Remove(filepath.Join(dir, "01_instance_types.yaml"))
		os.Remove(filepath.Join(dir, "02_instance_types.yaml"))
		os.Remove(dir)
	}
}

func TestInstanceTypesPraser(t *testing.T) {

	flavor, err := instanceParseType("c1-m2")
	if err != nil {
		t.Fatal(err)
	}

	if cpu, ok := flavor["limits.cpu"]; !ok || cpu != "1" {
		t.Fatal(flavor)
	}
	if mem, ok := flavor["limits.memory"]; !ok || mem != "2048MB" {
		t.Fatal(flavor)
	}

	dir, rollback := preperaInstanceTypesFile(t)
	defer rollback()

	err = instanceRefreshTypes(context.TODO(), &Daemon{}, dir, "")
	if err != nil {
		t.Fatal(err)
	}

	flavor, err = instanceParseType("gce:c1.xlarge")
	if err != nil {
		t.Fatal(err)
	}

	if cpu, ok := flavor["limits.cpu"]; !ok || cpu != "8" {
		t.Fatal(flavor)
	}
	if mem, ok := flavor["limits.memory"]; !ok || mem != "15360MB" {
		t.Fatal(flavor)
	}

	flavor, err = instanceParseType("aws:c1.medium")
	if err != nil {
		t.Fatal(err)
	}

	if cpu, ok := flavor["limits.cpu"]; !ok || cpu != "2" {
		t.Fatal(flavor)
	}
	if mem, ok := flavor["limits.memory"]; !ok || mem != "2048MB" {
		t.Fatal(flavor)
	}
}
