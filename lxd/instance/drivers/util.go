package drivers

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/units"
)

// parseMemoryStr parses a human-readable representation of a memory value.
func parseMemoryStr(memory string) (valueInt int64, err error) {
	if strings.HasSuffix(memory, "%") {
		var percent, memoryTotal int64

		percent, err = strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
		if err != nil {
			return 0, err
		}

		memoryTotal, err = shared.DeviceTotalMemory()
		if err != nil {
			return 0, err
		}

		valueInt = (memoryTotal / 100) * percent
	} else {
		valueInt, err = units.ParseByteSizeString(memory)
	}

	return valueInt, err
}

// ParseImageMetadataFile parses the specified YAML file into api.ImageMetadata.
// If the file exists, but is empty, then a zero value api.ImageMetadata is returned.
func ParseImageMetadataFile(path string) (*api.ImageMetadata, error) {
	metadataFile, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed reading metadata file %q: %w", path, err)
	}

	defer func() { _ = metadataFile.Close() }()

	metadata := new(api.ImageMetadata)
	err = yaml.NewDecoder(util.MaxBytesReader(metadataFile, util.MaxYAMLFileBytes)).Decode(metadata)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("Failed parsing metadata file %q: %w", path, err)
	}

	return metadata, nil
}
