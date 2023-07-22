package dnsmasq

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test_staticAllocationFileName verifies the generation of a file name based on project, instance, and device names.
func Test_staticAllocationFileName(t *testing.T) {
	projectName := "test.project"
	instanceName := "test-instance"
	deviceName := "test/.-_--.device"
	fileName := StaticAllocationFileName(projectName, instanceName, deviceName)
	assert.Equal(t, "test.project_test-instance.test-.--_----.device", fileName)
}
