package units

import (
	"testing"
)

func Test_handleOverflow(t *testing.T) {
	type args struct {
		val  int64
		mult int64
	}

	tests := []struct {
		name    string
		args    args
		want    int64
		wantErr bool
	}{
		{
			name: "no overflow",
			args: args{
				val:  2,
				mult: 3,
			},
			want:    6,
			wantErr: false,
		},
		{
			name: "overflow",
			args: args{
				val:  1 << 62,
				mult: 4,
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "zero multiplicator",
			args: args{
				val:  12345,
				mult: 0,
			},
			want:    0,
			wantErr: false,
		},
		{
			name: "zero value",
			args: args{
				val:  0,
				mult: 67890,
			},
			want:    0,
			wantErr: false,
		},
		{
			name: "one multiplicator",
			args: args{
				val:  12345,
				mult: 1,
			},
			want:    12345,
			wantErr: false,
		},
		{
			name: "one value",
			args: args{
				val:  1,
				mult: 67890,
			},
			want:    67890,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := handleOverflow(tt.args.val, tt.args.mult)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleOverflow() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("handleOverflow() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ParseByteSizeString(t *testing.T) {
	type args struct {
		input string
	}

	tests := []struct {
		name    string
		args    args
		want    int64
		wantErr bool
	}{
		{
			name: "empty input",
			args: args{
				input: "",
			},
			want:    0,
			wantErr: false,
		},
		{
			name: "valid bytes",
			args: args{
				input: "1024B",
			},
			want:    1024,
			wantErr: false,
		},
		{
			name: "valid kilobytes",
			args: args{
				input: "2kB",
			},
			want:    2000,
			wantErr: false,
		},
		{
			name: "valid megabytes",
			args: args{
				input: "3MB",
			},
			want:    3000000,
			wantErr: false,
		},
		{
			name: "valid gigabytes",
			args: args{
				input: "1GB",
			},
			want:    1000000000,
			wantErr: false,
		},
		{
			name: "valid terabytes",
			args: args{
				input: "1TB",
			},
			want:    1000000000000,
			wantErr: false,
		},
		{
			name: "valid petabytes",
			args: args{
				input: "1PB",
			},
			want:    1000000000000000,
			wantErr: false,
		},
		{
			name: "valid exabytes",
			args: args{
				input: "1EB",
			},
			want:    1000000000000000000,
			wantErr: false,
		},
		{
			name: "valid kibibytes",
			args: args{
				input: "2KiB",
			},
			want:    2048,
			wantErr: false,
		},
		{
			name: "valid mebibytes",
			args: args{
				input: "3MiB",
			},
			want:    3145728,
			wantErr: false,
		},
		{
			name: "valid gibibytes",
			args: args{
				input: "1GiB",
			},
			want:    1073741824,
			wantErr: false,
		},
		{
			name: "valid tebibytes",
			args: args{
				input: "1TiB",
			},
			want:    1099511627776,
			wantErr: false,
		},
		{
			name: "valid pebibytes",
			args: args{
				input: "1PiB",
			},
			want:    1125899906842624,
			wantErr: false,
		},
		{
			name: "valid exbibytes",
			args: args{
				input: "1EiB",
			},
			want:    1152921504606846976,
			wantErr: false,
		},
		{
			name: "invalid suffix",
			args: args{
				input: "123XY",
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "negative integer",
			args: args{
				input: "-123",
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "invalid integer",
			args: args{
				input: "12.34MB",
			},
			want:    -1,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseByteSizeString(tt.args.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseByteSizeString() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ParseByteSizeString() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ParseBitSizeString(t *testing.T) {
	type args struct {
		input string
	}

	tests := []struct {
		name    string
		args    args
		want    int64
		wantErr bool
	}{
		{
			name: "empty input",
			args: args{
				input: "",
			},
			want:    0,
			wantErr: false,
		},
		{
			name: "valid bits",
			args: args{
				input: "1024bit",
			},
			want:    1024,
			wantErr: false,
		},
		{
			name: "valid kilobits",
			args: args{
				input: "2kbit",
			},
			want:    2000,
			wantErr: false,
		},
		{
			name: "valid megabits",
			args: args{
				input: "3Mbit",
			},
			want:    3000000,
			wantErr: false,
		},
		{
			name: "valid gigabits",
			args: args{
				input: "1Gbit",
			},
			want:    1000000000,
			wantErr: false,
		},
		{
			name: "valid terabits",
			args: args{
				input: "1Tbit",
			},
			want:    1000000000000,
			wantErr: false,
		},
		{
			name: "valid petabits",
			args: args{
				input: "1Pbit",
			},
			want:    1000000000000000,
			wantErr: false,
		},
		{
			name: "valid exabits",
			args: args{
				input: "1Ebit",
			},
			want:    1000000000000000000,
			wantErr: false,
		},
		{
			name: "valid kibibits",
			args: args{
				input: "2Kibit",
			},
			want:    2048,
			wantErr: false,
		},
		{
			name: "valid mebibits",
			args: args{
				input: "3Mibit",
			},
			want:    3145728,
			wantErr: false,
		},
		{
			name: "valid gibibits",
			args: args{
				input: "1Gibit",
			},
			want:    1073741824,
			wantErr: false,
		},
		{
			name: "valid tebibits",
			args: args{
				input: "1Tibit",
			},
			want:    1099511627776,
			wantErr: false,
		},
		{
			name: "valid pebibits",
			args: args{
				input: "1Pibit",
			},
			want:    1125899906842624,
			wantErr: false,
		},
		{
			name: "valid exbibits",
			args: args{
				input: "1Eibit",
			},
			want:    1152921504606846976,
			wantErr: false,
		},
		{
			name: "invalid suffix",
			args: args{
				input: "123XY",
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "negative integer",
			args: args{
				input: "-123",
			},
			want:    -1,
			wantErr: true,
		},
		{
			name: "invalid integer",
			args: args{
				input: "12.34Mbit",
			},
			want:    -1,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBitSizeString(tt.args.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseBitSizeString() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("ParseBitSizeString() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_GetByteSizeString(t *testing.T) {
	type args struct {
		size int64
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "bytes",
			args: args{
				size: 512,
			},
			want: "512B",
		},
		{
			name: "kilobytes",
			args: args{
				size: 2048,
			},
			want: "2kB",
		},
		{
			name: "megabytes",
			args: args{
				size: 3 * 1000 * 1000,
			},
			want: "3MB",
		},
		{
			name: "gigabytes",
			args: args{
				size: 4 * 1000 * 1000 * 1000,
			},
			want: "4GB",
		},
		{
			name: "terabytes",
			args: args{
				size: 4 * 1000 * 1000 * 1000 * 1000,
			},
			want: "4TB",
		},
		{
			name: "petabytes",
			args: args{
				size: 4 * 1000 * 1000 * 1000 * 1000 * 1000,
			},
			want: "4PB",
		},
		{
			name: "exabytes",
			args: args{
				size: 9 * 1000 * 1000 * 1000 * 1000 * 1000 * 1000,
			},
			want: "9EB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetByteSizeString(tt.args.size, 0)
			if got != tt.want {
				t.Errorf("GetByteSizeString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_GetByteSizeStringIEC(t *testing.T) {
	type args struct {
		size int64
	}

	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "bytes",
			args: args{
				size: 512,
			},
			want: "512B",
		},
		{
			name: "kibibytes",
			args: args{
				size: 2048,
			},
			want: "2KiB",
		},
		{
			name: "mebibytes",
			args: args{
				size: 3 * 1024 * 1024,
			},
			want: "3MiB",
		},
		{
			name: "gibibytes",
			args: args{
				size: 4 * 1024 * 1024 * 1024,
			},
			want: "4GiB",
		},
		{
			name: "tebibytes",
			args: args{
				size: 4 * 1024 * 1024 * 1024 * 1024,
			},
			want: "4TiB",
		},
		{
			name: "pebibytes",
			args: args{
				size: 4 * 1024 * 1024 * 1024 * 1024 * 1024,
			},
			want: "4PiB",
		},
		{
			name: "exbibytes",
			args: args{
				size: 7 * 1024 * 1024 * 1024 * 1024 * 1024 * 1024,
			},
			want: "7EiB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetByteSizeStringIEC(tt.args.size, 0)
			if got != tt.want {
				t.Errorf("GetByteSizeStringIEC() = %v, want %v", got, tt.want)
			}
		})
	}
}
