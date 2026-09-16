package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImageRegistryValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names.
		{name: "simple", input: "images", wantErr: false},
		{name: "hyphenated", input: "ubuntu-daily", wantErr: false},
		{name: "multiple hyphens", input: "ubuntu-minimal-daily", wantErr: false},
		{name: "with dot", input: "my.registry", wantErr: false},
		{name: "with underscore", input: "my_registry", wantErr: false},
		{name: "numeric suffix", input: "registry123", wantErr: false},
		{name: "starts with digit", input: "1registry", wantErr: false},
		{name: "uppercase", input: "Registry", wantErr: false},
		{name: "single char", input: "a", wantErr: false},
		{name: "all allowed specials after alphanumeric", input: "a-b.c_d", wantErr: false},

		// Empty.
		{name: "empty", input: "", wantErr: true},

		// Invalid first character.
		{name: "leading hyphen", input: "-registry", wantErr: true},
		{name: "leading dot", input: ".registry", wantErr: true},
		{name: "leading underscore", input: "_registry", wantErr: true},

		// Disallowed characters.
		{name: "slash", input: "a/b", wantErr: true},
		{name: "colon", input: "a:b", wantErr: true},
		{name: "space", input: "my registry", wantErr: true},
		{name: "at sign", input: "reg@istry", wantErr: true},
		{name: "non-ASCII", input: "regïstry", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := imageRegistryValidateName(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
