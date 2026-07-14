package libkrun

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoaderSubprocess(t *testing.T) {
	if os.Getenv("GO_LIBKRUN_HELPER") != "1" {
		t.Skip("helper subprocess only")
	}

	switch os.Getenv("GO_LIBKRUN_CASE") {
	case "missing-lib":
		_, err := CreateContext()
		if err == nil {
			t.Fatalf("CreateContext() = nil error, want LoaderError")
		}

		var le LoaderError
		if !errors.As(err, &le) {
			t.Fatalf("CreateContext() error type = %T, want LoaderError", err)
		}

		if !strings.Contains(le.Error(), "dlopen(") {
			t.Fatalf("LoaderError = %q, want dlopen detail", le.Error())
		}

	case "missing-required-symbol":
		_, err := CreateContext()
		if err == nil {
			t.Fatalf("CreateContext() = nil error, want LoaderError")
		}

		var le LoaderError
		if !errors.As(err, &le) {
			t.Fatalf("CreateContext() error type = %T, want LoaderError", err)
		}

		if !strings.Contains(le.Error(), "required symbol") {
			t.Fatalf("LoaderError = %q, want missing required symbol detail", le.Error())
		}

	default:
		t.Fatalf("unknown GO_LIBKRUN_CASE=%q", os.Getenv("GO_LIBKRUN_CASE"))
	}
}

func TestLoaderMissingLibraryPath(t *testing.T) {
	runLoaderScenario(t, "missing-lib", filepath.Join(t.TempDir(), "does-not-exist-libkrun.so"))
}

func TestLoaderMissingRequiredSymbol(t *testing.T) {
	_, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not available")
	}

	libPath := buildSharedLibrary(t, "int not_krun(void) { return 0;}\n")
	runLoaderScenario(t, "missing-required-symbol", libPath)
}

func runLoaderScenario(t *testing.T, scenario string, libPath string) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run", "^TestLoaderSubprocess$")
	cmd.Env = append(os.Environ(),
		"GO_LIBKRUN_HELPER=1",
		"GO_LIBKRUN_CASE="+scenario,
		"LIBKRUN_PATH="+libPath,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scenario %q failed: %v\noutput:\n%s", scenario, err, string(out))
	}
}

func buildSharedLibrary(t *testing.T, source string) string {
	t.Helper()

	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "fakekrun.c")
	soPath := filepath.Join(tmp, "libkrun.so")

	err := os.WriteFile(srcPath, []byte(source), 0600)
	if err != nil {
		t.Fatalf("write source: %v", err)
	}

	cmd := exec.Command("cc", "-shared", "-fPIC", "-O2", "-o", soPath, srcPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake libkrun.so: %v\noutput:\n%s", err, string(out))
	}

	return soPath
}
