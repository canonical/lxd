package nodes

import (
	"errors"
	"strings"

	"github.com/r3labs/diff/v3"
)

type baseNode struct {
	data    any
	id      int64
	humanID string
}

func (bn *baseNode) ID() int64 {
	return bn.id
}

func (bn *baseNode) HumanID() string {
	return bn.humanID
}

func (bn *baseNode) Data() any {
	return bn.data
}

func (bn *baseNode) Renamable() bool {
	return false
}

func (bn *baseNode) Diff(other any) (diff.Changelog, error) {
	return nil, errors.New("Diff method not implemented for baseNode instance. The method needs overloading.")
}

func humanIDEncode(prefix string, parts ...string) string {
	encodedParts := make([]string, len(parts))
	for i, part := range parts {
		encodedParts[i] = strings.Replace(part, "_", "--", -1)
	}

	return prefix + strings.Join(encodedParts, "_")
}

func HumanIDDecode(humanID string) (prefix string, parts []string) {
	decodedParts := strings.Split(humanID, "_")
	prefix = decodedParts[0]
	for _, part := range decodedParts[1:] {
		parts = append(parts, strings.Replace(part, "--", "_", -1))
	}

	// 'root' is a special case
	if prefix == "root" {
		return prefix, []string{}
	}

	return prefix + "_", parts
}
