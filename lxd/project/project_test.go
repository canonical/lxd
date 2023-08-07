package project_test

import (
	"fmt"

	"github.com/canonical/lxd/lxd/project"
)

// Illustrates how to prefix instance names based on the project they belong to.
func ExampleInstance() {
	prefixed := project.Instance(project.Default, "test")
	fmt.Println(prefixed)

	prefixed = project.Instance("project_name", "test1")
	fmt.Println(prefixed)
	// Output: test
	// project_name_test1
}

// Demonstrates how to extract project and instance names from a prefixed instance identifier.
func ExampleInstanceParts() {
	projectName, name := project.InstanceParts("unprefixed")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance(project.Default, "test"))
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts("project_name_test")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Instance("proj", "test1"))
	fmt.Println(projectName, name)

	// Output: default unprefixed
	// default test
	// project_name test
	// proj test1
}

// Demonstrates how to format a storage volume name with project name prefix.
func ExampleStorageVolume() {
	prefixed := project.StorageVolume(project.Default, "test")
	fmt.Println(prefixed)

	prefixed = project.StorageVolume("project_name", "test1")
	fmt.Println(prefixed)
	// Output: default_test
	// project_name_test1
}
