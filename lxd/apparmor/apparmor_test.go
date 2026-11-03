package apparmor

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

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
