package lxd

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func Test_setQueryParam(t *testing.T) {
	type args struct {
		uri   string
		param string
		value string
	}

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "no existing params",
			args: args{
				uri:   "http://example.com",
				param: "foo",
				value: "bar",
			},
			want:    "http://example.com?foo=bar",
			wantErr: false,
		},
		{
			name: "existing params",
			args: args{
				uri:   "http://example.com?baz=qux",
				param: "foo",
				value: "bar",
			},
			want:    "http://example.com?baz=qux&foo=bar",
			wantErr: false,
		},
		{
			name: "overwrite existing param",
			args: args{
				uri:   "http://example.com?foo=old",
				param: "foo",
				value: "new",
			},
			want:    "http://example.com?foo=new",
			wantErr: false,
		},
		{
			name: "invalid URI",
			args: args{
				uri:   "http://%41:8080/", // Invalid percent-encoding
				param: "foo",
				value: "bar",
			},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := setQueryParam(tt.args.uri, tt.args.param, tt.args.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("setQueryParam() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("setQueryParam() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_urlsToResourceNames(t *testing.T) {
	type args struct {
		matchPrefix string
		urls        []string
	}

	tests := []struct {
		name        string
		args        args
		want        []string
		expectError bool
		err         error
	}{
		{
			name: "simple tests",
			args: args{
				matchPrefix: "1.0",
				urls: []string{
					"http://example.com/1.0/instances",
					"http://example.com/1.0/instances?recursion=1",
					"http://example.com/1.0/instances?recursion=1&project=default",
				},
			},
			want: []string{"instances", "instances", "instances"},
		},
		{
			name: "empty list",
			args: args{
				matchPrefix: "",
				urls:        []string{},
			},
			want: []string{},
		},
		{
			name: "no matching prefix",
			args: args{
				matchPrefix: "2.0",
				urls: []string{
					"http://example.com/1.0/instances",
					"http://example.com/1.0/instances?recursion=1",
				},
			},
			want:        []string{},
			expectError: true,
			err:         errors.New("Unexpected URL path"),
		},
		{
			name: "invalid URL",
			args: args{
				matchPrefix: "1.0",
				urls: []string{
					"http://%41/1.0/instances",
				},
			},
			want:        []string{},
			expectError: true,
			err:         errors.New("Failed parsing URL"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := urlsToResourceNames(tt.args.matchPrefix, tt.args.urls...)
			if (err != nil) != tt.expectError {
				t.Errorf("urlsToResourceNames() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError && err != nil && !strings.Contains(err.Error(), tt.err.Error()) {
				t.Errorf("urlsToResourceNames() error = %v, want %v", err, tt.err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("urlsToResourceNames() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_parseFilters(t *testing.T) {
	tests := []struct {
		name    string
		filters []string
		want    string
	}{
		{
			name:    "single filter",
			filters: []string{"key=value"},
			want:    "key eq value",
		},
		{
			name:    "multiple filters",
			filters: []string{"key=value", "foo=bar", "ignored"},
			want:    "key eq value and foo eq bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFilters(tt.filters)
			if got != tt.want {
				t.Errorf("parseFilters() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_openBrowser(t *testing.T) {
	tests := []struct {
		name string
		env  string
		url  string
	}{
		{
			name: "valid URL but none browser",
			env:  "none",
			url:  "http://example.com",
		},
		{
			name: "valid URL for a fake browser command",
			env:  "echo",
			url:  "http://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BROWSER", tt.env)
			err := openBrowser(tt.url)
			if err != nil {
				t.Errorf("openBrowser() unexpected error = %v", err)
			}
		})
	}
}
