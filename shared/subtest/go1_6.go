// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// See https://github.com/golang/go/blob/master/CONTRIBUTORS
// Licensed under the same terms as Go itself:
// https://github.com/golang/go/blob/master/LICENSE

// +build !go1.7

package subtest

import "testing"

// Run runs function f as a subtest of t.
func Run(t *testing.T, name string, f func(t *testing.T)) {
	t.Logf("Running %s...", name)
	f(t)
}
