package project_test

import (
	"fmt"

	"github.com/lxc/lxd/lxd/project"
)

func ExamplePrefix() {
	prefixed := project.Prefix(project.Default, "test")
	fmt.Println(prefixed)

	prefixed = project.Prefix("project_name", "test1")
	fmt.Println(prefixed)
	// Output: test
	// project_name_test1
}

func ExampleInstanceParts() {
	projectName, name := project.InstanceParts("unprefixed")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Prefix(project.Default, "test"))
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts("project_name_test")
	fmt.Println(projectName, name)

	projectName, name = project.InstanceParts(project.Prefix("proj", "test1"))
	fmt.Println(projectName, name)

	// Output: default unprefixed
	// default test
	// project_name test
	// proj test1
}
