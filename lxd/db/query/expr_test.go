package query

import (
	"testing"
)

func TestIntParams(t *testing.T) {
	tests := []struct {
		name string
		args []int
		want string
	}{
		{
			name: "empty",
			args: []int{},
			want: "()",
		},
		{
			name: "single int",
			args: []int{1},
			want: "(1)",
		},
		{
			name: "multiple int",
			args: []int{1, 2, 3},
			want: "(1, 2, 3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IntParams(tt.args...)
			if got != tt.want {
				t.Errorf("IntParams(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
