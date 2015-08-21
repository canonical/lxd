// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gnuflag_test

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	. "github.com/lxc/lxd/shared/gnuflag"
)

var (
	test_bool     = Bool("test_bool", false, "bool value")
	test_int      = Int("test_int", 0, "int value")
	test_int64    = Int64("test_int64", 0, "int64 value")
	test_uint     = Uint("test_uint", 0, "uint value")
	test_uint64   = Uint64("test_uint64", 0, "uint64 value")
	test_string   = String("test_string", "0", "string value")
	test_float64  = Float64("test_float64", 0, "float64 value")
	test_duration = Duration("test_duration", 0, "time.Duration value")
)

func boolString(s string) string {
	if s == "0" {
		return "false"
	}
	return "true"
}

func TestEverything(t *testing.T) {
	m := make(map[string]*Flag)
	desired := "0"
	visitor := func(f *Flag) {
		if len(f.Name) > 5 && f.Name[0:5] == "test_" {
			m[f.Name] = f
			ok := false
			switch {
			case f.Value.String() == desired:
				ok = true
			case f.Name == "test_bool" && f.Value.String() == boolString(desired):
				ok = true
			case f.Name == "test_duration" && f.Value.String() == desired+"s":
				ok = true
			}
			if !ok {
				t.Error("Visit: bad value", f.Value.String(), "for", f.Name)
			}
		}
	}
	VisitAll(visitor)
	if len(m) != 8 {
		t.Error("VisitAll misses some flags")
		for k, v := range m {
			t.Log(k, *v)
		}
	}
	m = make(map[string]*Flag)
	Visit(visitor)
	if len(m) != 0 {
		t.Errorf("Visit sees unset flags")
		for k, v := range m {
			t.Log(k, *v)
		}
	}
	// Now set all flags
	Set("test_bool", "true")
	Set("test_int", "1")
	Set("test_int64", "1")
	Set("test_uint", "1")
	Set("test_uint64", "1")
	Set("test_string", "1")
	Set("test_float64", "1")
	Set("test_duration", "1s")
	desired = "1"
	Visit(visitor)
	if len(m) != 8 {
		t.Error("Visit fails after set")
		for k, v := range m {
			t.Log(k, *v)
		}
	}
	// Now test they're visited in sort order.
	var flagNames []string
	Visit(func(f *Flag) { flagNames = append(flagNames, f.Name) })
	if !sort.StringsAreSorted(flagNames) {
		t.Errorf("flag names not sorted: %v", flagNames)
	}
}

func TestUsage(t *testing.T) {
	called := false
	ResetForTesting(func() { called = true })
	f := CommandLine()
	f.SetOutput(nullWriter{})
	if f.Parse(true, []string{"-x"}) == nil {
		t.Error("parse did not fail for unknown flag")
	}
	if called {
		t.Error("called Usage for unknown flag")
	}
}

var parseTests = []struct {
	about       string
	intersperse bool
	args        []string
	vals        map[string]interface{}
	remaining   []string
	error       string
}{{
	about:       "regular args",
	intersperse: true,
	args: []string{
		"--bool2",
		"--int", "22",
		"--int64", "0x23",
		"--uint", "24",
		"--uint64", "25",
		"--string", "hello",
		"--float64", "2718e28",
		"--duration", "2m",
		"one - extra - argument",
	},
	vals: map[string]interface{}{
		"bool":     false,
		"bool2":    true,
		"int":      22,
		"int64":    int64(0x23),
		"uint":     uint(24),
		"uint64":   uint64(25),
		"string":   "hello",
		"float64":  2718e28,
		"duration": 2 * 60 * time.Second,
	},
	remaining: []string{
		"one - extra - argument",
	},
}, {
	about:       "playing with -",
	intersperse: true,
	args: []string{
		"-a",
		"-",
		"-bc",
		"2",
		"-de1s",
		"-f2s",
		"-g", "3s",
		"--h",
		"--long",
		"--long2", "-4s",
		"3",
		"4",
		"--", "-5",
	},
	vals: map[string]interface{}{
		"a":     true,
		"b":     true,
		"c":     true,
		"d":     true,
		"e":     "1s",
		"f":     "2s",
		"g":     "3s",
		"h":     true,
		"long":  true,
		"long2": "-4s",
		"z":     "default",
		"www":   99,
	},
	remaining: []string{
		"-",
		"2",
		"3",
		"4",
		"-5",
	},
}, {
	about:       "flag after explicit --",
	intersperse: true,
	args: []string{
		"-a",
		"--",
		"-b",
	},
	vals: map[string]interface{}{
		"a": true,
		"b": false,
	},
	remaining: []string{
		"-b",
	},
}, {
	about: "flag after end",
	args: []string{
		"-a",
		"foo",
		"-b",
	},
	vals: map[string]interface{}{
		"a": true,
		"b": false,
	},
	remaining: []string{
		"foo",
		"-b",
	},
}, {
	about: "arg and flag after explicit end",
	args: []string{
		"-a",
		"--",
		"foo",
		"-b",
	},
	vals: map[string]interface{}{
		"a": true,
		"b": false,
	},
	remaining: []string{
		"foo",
		"-b",
	},
}, {
	about: "boolean args, explicitly and non-explicitly given",
	args: []string{
		"--a=false",
		"--b=true",
		"--c",
	},
	vals: map[string]interface{}{
		"a": false,
		"b": true,
		"c": true,
	},
}, {
	about: "using =",
	args: []string{
		"--arble=bar",
		"--bletch=",
		"--a=something",
		"-b=other",
		"-cdand more",
		"--curdle=--milk",
		"--sandwich", "=",
		"--darn=",
		"=arg",
	},
	vals: map[string]interface{}{
		"arble":    "bar",
		"bletch":   "",
		"a":        "something",
		"b":        "=other",
		"c":        true,
		"d":        "and more",
		"curdle":   "--milk",
		"sandwich": "=",
		"darn":     "",
	},
	remaining: []string{"=arg"},
}, {
	about: "empty flag #1",
	args: []string{
		"--=bar",
	},
	error: `empty flag in argument "--=bar"`,
}, {
	about: "single-letter equals",
	args: []string{
		"-=bar",
	},
	error: `flag provided but not defined: -=`,
}, {
	about: "empty flag #2",
	args: []string{
		"--=",
	},
	error: `empty flag in argument "--="`,
}, {
	about: "no equals",
	args: []string{
		"-=",
	},
	error: `flag provided but not defined: -=`,
}, {
	args: []string{
		"-a=true",
	},
	vals: map[string]interface{}{
		"a": true,
	},
	error: `invalid value "=true" for flag -a: strconv.ParseBool: parsing "=true": invalid syntax`,
}, {
	intersperse: true,
	args: []string{
		"-a",
		"-b",
	},
	vals: map[string]interface{}{
		"a": true,
	},
	error: "flag provided but not defined: -b",
}, {
	intersperse: true,
	args: []string{
		"-a",
	},
	vals: map[string]interface{}{
		"a": "default",
	},
	error: "flag needs an argument: -a",
}, {
	intersperse: true,
	args: []string{
		"-a", "b",
	},
	vals: map[string]interface{}{
		"a": 0,
	},
	error: `invalid value "b" for flag -a: strconv.ParseInt: parsing "b": invalid syntax`,
},
}

func testParse(newFlagSet func() *FlagSet, t *testing.T) {
	for i, g := range parseTests {
		t.Logf("test %d. %s", i, g.about)
		f := newFlagSet()
		flags := make(map[string]interface{})
		for name, val := range g.vals {
			switch val.(type) {
			case bool:
				flags[name] = f.Bool(name, false, "bool value "+name)
			case string:
				flags[name] = f.String(name, "default", "string value "+name)
			case int:
				flags[name] = f.Int(name, 99, "int value "+name)
			case uint:
				flags[name] = f.Uint(name, 0, "uint value")
			case uint64:
				flags[name] = f.Uint64(name, 0, "uint64 value")
			case int64:
				flags[name] = f.Int64(name, 0, "uint64 value")
			case float64:
				flags[name] = f.Float64(name, 0, "float64 value")
			case time.Duration:
				flags[name] = f.Duration(name, 5*time.Second, "duration value")
			default:
				t.Fatalf("unhandled type %T", val)
			}
		}
		err := f.Parse(g.intersperse, g.args)
		if g.error != "" {
			if err == nil {
				t.Errorf("expected error %q got nil", g.error)
			} else if err.Error() != g.error {
				t.Errorf("expected error %q got %q", g.error, err.Error())
			}
			continue
		}
		for name, val := range g.vals {
			actual := reflect.ValueOf(flags[name]).Elem().Interface()
			if val != actual {
				t.Errorf("flag %q, expected %v got %v", name, val, actual)
			}
		}
		if len(f.Args()) != len(g.remaining) {
			t.Fatalf("remaining args, expected %q got %q", g.remaining, f.Args())
		}
		for j, a := range f.Args() {
			if a != g.remaining[j] {
				t.Errorf("arg %d, expected %q got %q", j, g.remaining[i], a)
			}
		}
	}
}

func TestParse(t *testing.T) {
	testParse(func() *FlagSet {
		ResetForTesting(func() {})
		f := CommandLine()
		f.SetOutput(nullWriter{})
		return f
	}, t)
}

func TestFlagSetParse(t *testing.T) {
	testParse(func() *FlagSet {
		f := NewFlagSet("test", ContinueOnError)
		f.SetOutput(nullWriter{})
		return f
	}, t)
}

// Declare a user-defined flag type.
type flagVar []string

func (f *flagVar) String() string {
	return fmt.Sprint([]string(*f))
}

func (f *flagVar) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func TestUserDefined(t *testing.T) {
	var flags FlagSet
	flags.Init("test", ContinueOnError)
	var v flagVar
	flags.Var(&v, "v", "usage")
	if err := flags.Parse(true, []string{"-v", "1", "-v", "2", "-v3"}); err != nil {
		t.Error(err)
	}
	if len(v) != 3 {
		t.Fatal("expected 3 args; got ", len(v))
	}
	expect := "[1 2 3]"
	if v.String() != expect {
		t.Errorf("expected value %q got %q", expect, v.String())
	}
}

func TestSetOutput(t *testing.T) {
	var flags FlagSet
	var buf bytes.Buffer
	flags.SetOutput(&buf)
	flags.Init("test", ContinueOnError)
	flags.Parse(true, []string{"-unknown"})
	if out := buf.String(); !strings.Contains(out, "-unknown") {
		t.Logf("expected output mentioning unknown; got %q", out)
	}
}

// This tests that one can reset the flags. This still works but not well, and is
// superseded by FlagSet.
func TestChangingArgs(t *testing.T) {
	ResetForTesting(func() { t.Fatal("bad parse") })
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"cmd", "--before", "subcmd", "--after", "args"}
	before := Bool("before", false, "")
	if err := CommandLine().Parse(false, os.Args[1:]); err != nil {
		t.Fatal(err)
	}
	cmd := Arg(0)
	os.Args = Args()
	after := Bool("after", false, "")
	Parse(false)
	args := Args()

	if !*before || cmd != "subcmd" || !*after || len(args) != 1 || args[0] != "args" {
		t.Fatalf("expected true subcmd true [args] got %v %v %v %v", *before, cmd, *after, args)
	}
}

// Test that -help invokes the usage message and returns ErrHelp.
func TestHelp(t *testing.T) {
	var helpCalled = false
	fs := NewFlagSet("help test", ContinueOnError)
	fs.SetOutput(nullWriter{})
	fs.Usage = func() { helpCalled = true }
	var flag bool
	fs.BoolVar(&flag, "flag", false, "regular flag")
	// Regular flag invocation should work
	err := fs.Parse(true, []string{"--flag"})
	if err != nil {
		t.Fatal("expected no error; got ", err)
	}
	if !flag {
		t.Error("flag was not set by --flag")
	}
	if helpCalled {
		t.Error("help called for regular flag")
		helpCalled = false // reset for next test
	}
	// Help flag should work as expected.
	err = fs.Parse(true, []string{"--help"})
	if err == nil {
		t.Fatal("error expected")
	}
	if err != ErrHelp {
		t.Fatal("expected ErrHelp; got ", err)
	}
	if !helpCalled {
		t.Fatal("help was not called")
	}
	// If we define a help flag, that should override.
	var help bool
	fs.BoolVar(&help, "help", false, "help flag")
	helpCalled = false
	err = fs.Parse(true, []string{"--help"})
	if err != nil {
		t.Fatal("expected no error for defined --help; got ", err)
	}
	if helpCalled {
		t.Fatal("help was called; should not have been for defined help flag")
	}
}

type nullWriter struct{}

func (nullWriter) Write(buf []byte) (int, error) {
	return len(buf), nil
}

func TestPrintDefaults(t *testing.T) {
	f := NewFlagSet("print test", ContinueOnError)
	f.SetOutput(nullWriter{})
	var b bool
	var c int
	var d string
	var e float64
	f.IntVar(&c, "trapclap", 99, "usage not shown")
	f.IntVar(&c, "c", 99, "c usage")

	f.BoolVar(&b, "bal", false, "usage not shown")
	f.BoolVar(&b, "x", false, "usage not shown")
	f.BoolVar(&b, "b", false, "b usage")
	f.BoolVar(&b, "balalaika", false, "usage not shown")

	f.StringVar(&d, "d", "d default", "d usage")

	f.Float64Var(&e, "elephant", 3.14, "elephant usage")

	var buf bytes.Buffer
	f.SetOutput(&buf)
	f.PrintDefaults()
	f.SetOutput(nullWriter{})

	expect :=
		`    -b, -x, --bal, --balalaika  (= false)
        b usage
    -c, --trapclap  (= 99)
        c usage
    -d (= "d default")
        d usage
    --elephant  (= 3.14)
        elephant usage
`
	if buf.String() != expect {
		t.Errorf("expect %q got %q", expect, buf.String())
	}
}
