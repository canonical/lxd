package encryption

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_deriveKey(t *testing.T) {
	type args struct {
		secret         []byte
		salt           []byte
		usageSeparator string
		length         uint
	}

	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr error
	}{
		{
			name: "nil salt",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, 64),
				salt:           nil,
				usageSeparator: "TEST",
				length:         32,
			},
			wantErr: errInsufficientSalt,
		},
		{
			name: "salt too short",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, 64),
				salt:           slices.Repeat([]byte{'0'}, 15),
				usageSeparator: "TEST",
				length:         32,
			},
			wantErr: errInsufficientSalt,
		},
		{
			name: "empty usage",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, 64),
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "",
				length:         32,
			},
			wantErr: errNoUsage,
		},
		{
			name: "key too short",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, 64),
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "TEST",
				length:         31,
			},
			wantErr: errKeyTooShort,
		},
		{
			name: "key too long",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, 64),
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "TEST",
				length:         uint(hashFunc().Size() + 1),
			},
			wantErr: errKeyTooLong,
		},
		{
			name: "nil secret",
			args: args{
				secret:         nil,
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "TEST",
				length:         uint(hashFunc().Size()),
			},
			wantErr: errSecretTooShort,
		},
		{
			name: "secret too short",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, 63),
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "TEST",
				length:         uint(hashFunc().Size()),
			},
			wantErr: errSecretTooShort,
		},
		{
			name: "valid, max length",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, hashFunc().Size()),
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "TEST",
				length:         uint(hashFunc().Size()),
			},
			want: []byte{0xc6, 0x8a, 0x4a, 0x97, 0x41, 0x73, 0x8c, 0xe3, 0x34, 0xf9, 0x55, 0xc2, 0x31, 0x7f, 0x97, 0xf9, 0x2e, 0xd9, 0xcc, 0x4a, 0x7c, 0xa1, 0xa9, 0xf2, 0xd7, 0x5, 0xbb, 0xa5, 0x7c, 0x33, 0xb6, 0x1c, 0x1c, 0xdd, 0x77, 0xbf, 0x97, 0xd0, 0xd, 0x24, 0x50, 0x23, 0xb2, 0xe2, 0x4f, 0x7b, 0x5b, 0x14, 0xb2, 0xc4, 0x6, 0x11, 0x39, 0x95, 0x52, 0x59, 0x55, 0xa2, 0x79, 0x2d, 0xcf, 0x79, 0x15, 0x7a},
		},
		{
			name: "valid, length 32",
			args: args{
				secret:         slices.Repeat([]byte{'0'}, hashFunc().Size()),
				salt:           slices.Repeat([]byte{'0'}, 16),
				usageSeparator: "TEST",
				length:         32,
			},
			want: []byte{0xc6, 0x8a, 0x4a, 0x97, 0x41, 0x73, 0x8c, 0xe3, 0x34, 0xf9, 0x55, 0xc2, 0x31, 0x7f, 0x97, 0xf9, 0x2e, 0xd9, 0xcc, 0x4a, 0x7c, 0xa1, 0xa9, 0xf2, 0xd7, 0x5, 0xbb, 0xa5, 0x7c, 0x33, 0xb6, 0x1c},
		},
	}

	for _, tt := range tests {
		got, err := deriveKey(tt.args.secret, tt.args.salt, tt.args.usageSeparator, tt.args.length)
		assert.Equal(t, tt.wantErr, err)
		assert.Equal(t, tt.want, got)
	}
}
