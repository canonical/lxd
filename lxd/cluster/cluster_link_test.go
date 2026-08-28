package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddressSetChanged(t *testing.T) {
	tests := []struct {
		name    string
		current []string
		updated []string
		want    bool
	}{
		{
			name:    "identical slices",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    false,
		},
		{
			name:    "same addresses different order",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.2:8443", "10.0.0.1:8443"},
			want:    false,
		},
		{
			name:    "address added",
			current: []string{"10.0.0.1:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			want:    true,
		},
		{
			name:    "address removed",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "address replaced",
			current: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			updated: []string{"10.0.0.1:8443", "10.0.0.3:8443"},
			want:    true,
		},
		{
			name:    "both empty",
			current: []string{},
			updated: []string{},
			want:    false,
		},
		{
			name:    "current empty updated non-empty",
			current: []string{},
			updated: []string{"10.0.0.1:8443"},
			want:    true,
		},
		{
			name:    "current non-empty updated empty",
			current: []string{"10.0.0.1:8443"},
			updated: []string{},
			want:    true,
		},
		{
			name:    "nil slices",
			current: nil,
			updated: nil,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := addressSetChanged(tc.current, tc.updated)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrioritizeResponsiveClusterLinkAddresses(t *testing.T) {
	tests := []struct {
		name      string
		addresses []string
		probe     func(ctx context.Context, address string) error
		want      []string
	}{
		{
			name:      "moves first responsive address to front",
			addresses: []string{"10.0.0.1:8443", "10.0.0.2:8443", "10.0.0.3:8443"},
			probe: func(ctx context.Context, address string) error {
				if address == "10.0.0.2:8443" {
					return nil
				}

				<-ctx.Done()
				return ctx.Err()
			},
			want: []string{"10.0.0.2:8443", "10.0.0.1:8443", "10.0.0.3:8443"},
		},
		{
			name:      "keeps order when first address is already responsive",
			addresses: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			probe: func(ctx context.Context, address string) error {
				if address == "10.0.0.1:8443" {
					return nil
				}

				<-ctx.Done()
				return ctx.Err()
			},
			want: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
		},
		{
			name:      "keeps original order when no address responds",
			addresses: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
			probe: func(ctx context.Context, address string) error {
				return errors.New("failed connecting")
			},
			want: []string{"10.0.0.1:8443", "10.0.0.2:8443"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := prioritizeResponsiveClusterLinkAddresses(context.Background(), tc.addresses, tc.probe)
			assert.Equal(t, tc.want, got)
		})
	}
}
