package apparmor

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/canonical/lxd/lxd/sys"
)

func TestForkstartProfile(t *testing.T) {
	const name = "lxd_forkstart-1234"

	profile, err := forkstartProfile(name, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, rule := range []string{
		`profile "` + name + `" flags=(attach_disconnected,mediate_deleted) {`,
		"  capability,",
		"  file,",
		"  mount,",
		"  umount,",
		"  pivot_root,",
		"  network,",
		"  unix,",
		"  ptrace,",
		"  signal,",
		"  dbus,",
		"  userns,",
		"  change_profile -> lxd-**,",
		`  change_profile -> ":lxd-**:*",`,
	} {
		if !strings.Contains(profile, rule) {
			t.Errorf("Profile is missing %q", rule)
		}
	}

	if strings.Contains(profile, "flags=(unconfined)") {
		t.Error("Forkstart profile must be enforced")
	}

	profile, err = forkstartProfile(name, false)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(profile, "  userns,") {
		t.Error("Profile contains a userns rule when the parser does not support it")
	}
}

func TestForkstartWrapper(t *testing.T) {
	testDir := t.TempDir()
	binDir := filepath.Join(testDir, "bin")
	err := os.Mkdir(binDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	parserLog := filepath.Join(testDir, "apparmor_parser.log")
	for _, name := range []string{"aa-exec", "apparmor_parser"} {
		body := "#!/bin/sh\nexit 0\n"
		if name == "apparmor_parser" {
			body = "#!/bin/sh\necho \"$@\" >> \"" + parserLog + "\"\n"
		}

		err = os.WriteFile(filepath.Join(binDir, name), []byte(body), 0755)
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", binDir)

	oldAAPath := aaPath
	aaPath = filepath.Join(testDir, "apparmor")
	t.Cleanup(func() { aaPath = oldAAPath })

	err = os.MkdirAll(filepath.Join(aaPath, "profiles"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := filepath.Join(testDir, "cache")
	err = os.MkdirAll(cacheDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	sysOS := &sys.OS{
		VarDir:            filepath.Join(testDir, "lxd"),
		AppArmorAvailable: true,
		AppArmorAdmin:     true,
		AppArmorCacheLoc:  cacheDir,
		AppArmorCacheDir:  cacheDir,
	}

	sysOS.AppArmorFeatures.Map = map[string]bool{"userns_rule": true}

	err = ForkstartLoad(sysOS)
	if err != nil {
		t.Fatal(err)
	}

	wantProfileName := forkstartProfileName(sysOS)
	if !strings.HasPrefix(wantProfileName, "lxd_forkstart-<") {
		t.Errorf("Profile name = %q, want lxd_forkstart-<...> prefix", wantProfileName)
	}

	extraFile, err := os.CreateTemp(testDir, "extra")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = extraFile.Close() })

	cmd := exec.Command("/opt/lxd/bin/lxd", "forkstart", "c1", "/var/lib/lxd", "/tmp/lxc.conf")
	cmd.Env = []string{"LXC_MEMFD_REXEC=1"}
	cmd.ExtraFiles = []*os.File{extraFile}
	err = ForkstartWrapper(sysOS, cmd)
	if err != nil {
		t.Fatal(err)
	}

	if cmd.Path != filepath.Join(binDir, "aa-exec") {
		t.Errorf("Command path = %q, want %q", cmd.Path, filepath.Join(binDir, "aa-exec"))
	}

	wantArgs := []string{"aa-exec", "-p", wantProfileName, "/opt/lxd/bin/lxd", "forkstart", "c1", "/var/lib/lxd", "/tmp/lxc.conf"}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Errorf("Command args = %q, want %q", cmd.Args, wantArgs)
	}

	if !slices.Equal(cmd.Env, []string{"LXC_MEMFD_REXEC=1"}) {
		t.Errorf("Command environment = %q, want LXC_MEMFD_REXEC=1", cmd.Env)
	}

	if len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] != extraFile {
		t.Error("Command inherited file descriptors were not preserved")
	}

	profilePath := filepath.Join(aaPath, "profiles", forkstartProfileFilename)
	err = os.Chtimes(profilePath, time.Unix(1, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}

	err = ForkstartWrapper(sysOS, exec.Command("/opt/lxd/bin/lxd", "forkexec"))
	if err != nil {
		t.Fatal(err)
	}

	parserCalls, err := os.ReadFile(parserLog)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Count(string(parserCalls), "\n") != 1 {
		t.Errorf("Profile was loaded %d times, want once", strings.Count(string(parserCalls), "\n"))
	}

	info, err := os.Stat(profilePath)
	if err != nil {
		t.Fatal(err)
	}

	if !info.ModTime().Equal(time.Unix(1, 0)) {
		t.Errorf("Unchanged profile mtime was modified: %s", info.ModTime())
	}

	cachePath := filepath.Join(cacheDir, forkstartProfileFilename)
	err = os.WriteFile(cachePath, []byte("cached"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	err = ForkstartDelete(sysOS)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{profilePath, cachePath} {
		_, err = os.Stat(p)
		if !os.IsNotExist(err) {
			t.Errorf("Expected %q to be removed, got err=%v", p, err)
		}
	}
}

func TestForkstartWrapperFallback(t *testing.T) {
	originalArgs := []string{"/opt/lxd/bin/lxd", "forkconsole", "c1"}

	tests := []struct {
		name  string
		sysOS *sys.OS
	}{
		{
			name:  "AppArmor unavailable",
			sysOS: &sys.OS{AppArmorAdmin: true},
		},
		{
			name:  "AppArmor administration unavailable",
			sysOS: &sys.OS{AppArmorAvailable: true},
		},
		{
			name:  "daemon already confined",
			sysOS: &sys.OS{AppArmorAvailable: true, AppArmorAdmin: true, AppArmorConfined: true},
		},
		{
			name:  "AppArmor namespace already stacked",
			sysOS: &sys.OS{AppArmorAvailable: true, AppArmorAdmin: true, AppArmorStacked: true},
		},
		{
			name:  "aa-exec unavailable",
			sysOS: &sys.OS{AppArmorAvailable: true, AppArmorAdmin: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			t.Setenv("PATH", binDir)

			cmd := exec.Command(originalArgs[0], originalArgs[1:]...)
			err := ForkstartWrapper(tt.sysOS, cmd)
			if err != nil {
				t.Fatal(err)
			}

			if cmd.Path != originalArgs[0] {
				t.Errorf("Command path = %q, want %q", cmd.Path, originalArgs[0])
			}

			if !slices.Equal(cmd.Args, originalArgs) {
				t.Errorf("Command args = %q, want %q", cmd.Args, originalArgs)
			}
		})
	}
}

func TestLDLibraryPaths(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{"empty", "", []string{}},
		{"only separators", ":::", []string{}},
		{"absolute entries", "/a/b:/c", []string{"/a/b", "/c"}},
		{"trailing slash is removed", "/a/b/", []string{"/a/b"}},
		{"dot dot is resolved", "/a/../b:/c/./d", []string{"/b", "/c/d"}},
		{"empty entries", ":/a::/b:", []string{"/a", "/b"}},
		{"relative entries", ".:a/b:../c:/d", []string{"/d"}},
		{"root directory", "/://:/.:/a", []string{"/a"}},
		{"duplicates", "/a:/a", []string{"/a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LD_LIBRARY_PATH", tt.value)

			got := ldLibraryPaths()
			if !slices.Equal(got, tt.want) {
				t.Errorf("ldLibraryPaths() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLibraryPathEntries(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		origin string
		want   []string
	}{
		{"empty", "", "/opt/lxd/bin", []string{}},
		{"origin", "$ORIGIN/lib:${ORIGIN}/other:/a", "/opt/lxd/bin", []string{"/opt/lxd/bin/lib", "/opt/lxd/bin/other", "/a"}},
		{"origin with dot dot", "$ORIGIN/../lib:/a", "/opt/lxd/bin", []string{"/opt/lxd/lib", "/a"}},
		{"origin without value", "$ORIGIN/lib:/a", "", []string{"/a"}},
		{"unexpanded token", "$LIB:/a", "/opt/lxd/bin", []string{"/a"}},
		{"origin as a prefix", "$ORIGINAL/lib:$ORIGIN_X:$ORIGIN2:/a", "/opt/lxd/bin", []string{"/a"}},
		{"origin alone", "$ORIGIN:${ORIGIN}", "/opt/lxd/bin", []string{"/opt/lxd/bin"}},
		{"origin with special characters", "$ORIGIN/lib", "/opt/$1", []string{}},
		{"root after origin", "$ORIGIN/..", "/", []string{}},
		{"duplicates across tokens", "$ORIGIN/lib:/opt/lxd/bin/lib", "/opt/lxd/bin", []string{"/opt/lxd/bin/lib"}},
		{"trailing slash without origin", "/a/b/", "", []string{"/a/b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := libraryPathEntries(tt.value, tt.origin)
			if !slices.Equal(got, tt.want) {
				t.Errorf("libraryPathEntries() = %q, want %q", got, tt.want)
			}
		})
	}
}

// writeELF writes a minimal ELF file carrying the given dynamic search paths,
// so that tests do not depend on how a real binary was linked.
func writeELF(t *testing.T, rpaths []string, runpaths []string) string {
	t.Helper()

	const headerSize = 64
	const sectionSize = 64

	// Dynamic string table, which starts with the empty string.
	dynstr := []byte{0}

	// Dynamic section entries, each a tag and a string table offset.
	dynamic := []byte{}
	addEntry := func(tag elf.DynTag, value string) {
		offset := uint32(len(dynstr))
		dynstr = append(dynstr, value...)
		dynstr = append(dynstr, 0)

		entry := make([]byte, 16)
		binary.LittleEndian.PutUint64(entry[0:8], uint64(tag))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(offset))
		dynamic = append(dynamic, entry...)
	}

	for _, value := range rpaths {
		addEntry(elf.DT_RPATH, value)
	}

	for _, value := range runpaths {
		addEntry(elf.DT_RUNPATH, value)
	}

	dynamic = append(dynamic, make([]byte, 16)...) // DT_NULL

	path := filepath.Join(t.TempDir(), "lxd-test-elf")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = f.Close() }()

	// The ELF64 header is written once the offsets are known.
	header := make([]byte, headerSize)
	copy(header[0:4], "\x7fELF")
	header[4] = byte(elf.ELFCLASS64)
	header[5] = byte(elf.ELFDATA2LSB)
	header[6] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(header[16:], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(header[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(header[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(header[52:], headerSize)
	binary.LittleEndian.PutUint16(header[58:], sectionSize)

	dynstrOff := uint64(headerSize)
	dynamicOff := dynstrOff + uint64(len(dynstr))
	sectionOff := dynamicOff + uint64(len(dynamic))
	binary.LittleEndian.PutUint64(header[40:], sectionOff)
	binary.LittleEndian.PutUint16(header[60:], 3) // Null, string table, dynamic.

	// Section headers: the mandatory null one, the string table and the
	// dynamic section, which links to the string table.
	sections := make([]byte, 3*sectionSize)
	binary.LittleEndian.PutUint32(sections[sectionSize+4:], uint32(elf.SHT_STRTAB))
	binary.LittleEndian.PutUint64(sections[sectionSize+24:], dynstrOff)
	binary.LittleEndian.PutUint64(sections[sectionSize+32:], uint64(len(dynstr)))
	binary.LittleEndian.PutUint64(sections[sectionSize+48:], 1)

	dynamicSection := sections[2*sectionSize:]
	binary.LittleEndian.PutUint32(dynamicSection[4:], uint32(elf.SHT_DYNAMIC))
	binary.LittleEndian.PutUint64(dynamicSection[24:], dynamicOff)
	binary.LittleEndian.PutUint64(dynamicSection[32:], uint64(len(dynamic)))
	binary.LittleEndian.PutUint32(dynamicSection[40:], 1) // Link to the string table.
	binary.LittleEndian.PutUint64(dynamicSection[56:], 16)

	_, err = f.Write(header)
	if err != nil {
		t.Fatal(err)
	}

	for _, blob := range [][]byte{dynstr, dynamic, sections} {
		_, err = f.Write(blob)
		if err != nil {
			t.Fatal(err)
		}
	}

	return path
}

func TestExeLibraryPaths(t *testing.T) {
	_, err := exeLibraryPaths(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Error("exeLibraryPaths() on a missing file did not return an error")
	}

	// The test binary has a dynamic section but no DT_RPATH or DT_RUNPATH, so
	// this covers a binary that names no library search path of its own.
	exe, err := filepath.EvalSymlinks("/proc/self/exe")
	if err != nil {
		t.Fatalf("Failed resolving the test binary: %v", err)
	}

	paths, err := exeLibraryPaths(exe)
	if err != nil {
		t.Fatalf("exeLibraryPaths(%q) failed: %v", exe, err)
	}

	for _, path := range paths {
		if !filepath.IsAbs(path) || path == "/" || filepath.Clean(path) != path {
			t.Errorf("exeLibraryPaths() returned unusable path %q", path)
		}
	}

	exe = writeELF(t, []string{"$ORIGIN/../lib:/opt/rpath"}, []string{"${ORIGIN}/runpath"})

	paths, err = exeLibraryPaths(exe)
	if err != nil {
		t.Fatalf("exeLibraryPaths(%q) failed: %v", exe, err)
	}

	want := []string{filepath.Join(filepath.Dir(exe), "..", "lib"), "/opt/rpath", filepath.Join(filepath.Dir(exe), "runpath")}
	if !slices.Equal(paths, want) {
		t.Errorf("exeLibraryPaths() = %q, want %q", paths, want)
	}
}

func TestProfileLibraryPaths(t *testing.T) {
	t.Setenv("LD_LIBRARY_PATH", "/a:/b")

	// An unreadable binary still yields the LD_LIBRARY_PATH entries.
	got := profileLibraryPaths(filepath.Join(t.TempDir(), "missing"))
	if !slices.Equal(got, []string{"/a", "/b"}) {
		t.Errorf("profileLibraryPaths() = %q, want %q", got, []string{"/a", "/b"})
	}
}
