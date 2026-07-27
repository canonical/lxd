package drivers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	yaml "go.yaml.in/yaml/v2"

	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/osarch"
)

// newTemplateTestInstance builds a minimal *lxc instance backed by an on-disk
// layout under a temporary LXD_DIR, suitable for exercising templateApplyNow.
// It writes the provided metadata to metadata.yaml and returns the instance
// along with the paths of its rootfs and templates directories.
func newTemplateTestInstance(t *testing.T, meta api.ImageMetadata) (d *lxc, rootfsDir string, templatesDir string) {
	t.Helper()

	lxdDir := t.TempDir()
	t.Setenv("LXD_DIR", lxdDir)

	// project.Instance() does not prefix instance names in the default project,
	// so the on-disk directory is simply containers/<name>.
	const instName = "c1"
	instDir := filepath.Join(lxdDir, "containers", instName)

	rootfsDir = filepath.Join(instDir, "rootfs")
	templatesDir = filepath.Join(instDir, "templates")

	for _, dir := range []string{instDir, rootfsDir, templatesDir} {
		err := os.MkdirAll(dir, 0755)
		if err != nil {
			t.Fatalf("Failed creating instance directory %q: %v", dir, err)
		}
	}

	metaBytes, err := yaml.Marshal(meta)
	if err != nil {
		t.Fatalf("Failed marshalling metadata: %v", err)
	}

	err = os.WriteFile(filepath.Join(instDir, "metadata.yaml"), metaBytes, 0644)
	if err != nil {
		t.Fatalf("Failed writing metadata.yaml: %v", err)
	}

	// Map the container root (uid/gid 0) onto the current user so that the
	// ownership fixups performed by templateApplyNow (which chown created files
	// and directories to the mapped root) succeed when the test runs unprivileged.
	uid := int64(os.Getuid())
	gid := int64(os.Getgid())
	idmapJSON, err := json.Marshal([]idmap.IdmapEntry{
		{Isuid: true, Nsid: 0, Hostid: uid, Maprange: 1},
		{Isgid: true, Nsid: 0, Hostid: gid, Maprange: 1},
	})
	if err != nil {
		t.Fatalf("Failed marshalling idmap: %v", err)
	}

	d = &lxc{
		common: common{
			architecture:    osarch.ARCH_64BIT_INTEL_X86,
			dbType:          instancetype.Container,
			name:            instName,
			project:         api.Project{Name: api.ProjectDefaultName},
			expandedConfig:  map[string]string{},
			expandedDevices: deviceConfig.Devices{},
			localConfig:     map[string]string{"volatile.last_state.idmap": string(idmapJSON)},
		},
	}

	return d, rootfsDir, templatesDir
}

// writeTemplateSource writes a template source file into the templates directory.
func writeTemplateSource(t *testing.T, templatesDir string, name string, content string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(templatesDir, name), []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed writing template source %q: %v", name, err)
	}
}

// TestTemplateApplyNowRendering covers the functional behaviour of the template
// loop: trigger filtering, rendering, create-only handling and overwriting.
func TestTemplateApplyNowRendering(t *testing.T) {
	t.Run("renders template into rootfs file", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc/hello": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "hello.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		d.expandedConfig["user.greeting"] = "world"
		writeTemplateSource(t, templatesDir, "hello.tpl", `hello {{ config_get("user.greeting", "none") }}`)

		err := d.templateApplyNow(instance.TemplateTriggerCreate)
		if err != nil {
			t.Fatalf("templateApplyNow returned error: %v", err)
		}

		target := filepath.Join(rootfsDir, "etc", "hello")
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("Failed reading rendered file: %v", err)
		}

		if string(content) != "hello world" {
			t.Fatalf("Unexpected rendered content: %q", string(content))
		}

		fi, err := os.Stat(target)
		if err != nil {
			t.Fatalf("Failed stating rendered file: %v", err)
		}

		if fi.Mode().Perm() != 0644 {
			t.Fatalf("Unexpected file mode: %v", fi.Mode().Perm())
		}
	})

	t.Run("creates missing parent directories", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/sub/dir/file": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "file.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "file.tpl", "content")

		err := d.templateApplyNow(instance.TemplateTriggerCreate)
		if err != nil {
			t.Fatalf("templateApplyNow returned error: %v", err)
		}

		content, err := os.ReadFile(filepath.Join(rootfsDir, "sub", "dir", "file"))
		if err != nil {
			t.Fatalf("Failed reading rendered file: %v", err)
		}

		if string(content) != "content" {
			t.Fatalf("Unexpected rendered content: %q", string(content))
		}
	})

	t.Run("skips template when trigger does not match", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc/skip": {
					When:     []string{string(instance.TemplateTriggerCopy)},
					Template: "skip.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "skip.tpl", "content")

		err := d.templateApplyNow(instance.TemplateTriggerCreate)
		if err != nil {
			t.Fatalf("templateApplyNow returned error: %v", err)
		}

		_, err = os.Stat(filepath.Join(rootfsDir, "etc", "skip"))
		if !os.IsNotExist(err) {
			t.Fatalf("Expected target file to not exist, got err: %v", err)
		}
	})

	t.Run("create_only skips existing target", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc/keep": {
					When:       []string{string(instance.TemplateTriggerCreate)},
					Template:   "keep.tpl",
					CreateOnly: true,
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "keep.tpl", "new content")

		target := filepath.Join(rootfsDir, "etc", "keep")
		err := os.MkdirAll(filepath.Dir(target), 0755)
		if err != nil {
			t.Fatalf("Failed creating target dir: %v", err)
		}

		err = os.WriteFile(target, []byte("original"), 0644)
		if err != nil {
			t.Fatalf("Failed writing existing target: %v", err)
		}

		err = d.templateApplyNow(instance.TemplateTriggerCreate)
		if err != nil {
			t.Fatalf("templateApplyNow returned error: %v", err)
		}

		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("Failed reading target: %v", err)
		}

		if string(content) != "original" {
			t.Fatalf("Expected create_only target to be unchanged, got: %q", string(content))
		}
	})

	t.Run("overwrites existing target when not create_only", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc/overwrite": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "overwrite.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "overwrite.tpl", "updated")

		target := filepath.Join(rootfsDir, "etc", "overwrite")
		err := os.MkdirAll(filepath.Dir(target), 0755)
		if err != nil {
			t.Fatalf("Failed creating target dir: %v", err)
		}

		err = os.WriteFile(target, []byte("stale longer content"), 0644)
		if err != nil {
			t.Fatalf("Failed writing existing target: %v", err)
		}

		err = d.templateApplyNow(instance.TemplateTriggerCreate)
		if err != nil {
			t.Fatalf("templateApplyNow returned error: %v", err)
		}

		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("Failed reading target: %v", err)
		}

		if string(content) != "updated" {
			t.Fatalf("Expected target to be overwritten and truncated, got: %q", string(content))
		}
	})
}

// TestTemplateApplyNowSecurity covers the escape and safety protections in the
// template loop: symlink source rejection, path escape confinement, invalid
// target paths and directory targets.
func TestTemplateApplyNowSecurity(t *testing.T) {
	t.Run("rejects symlink template source", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc/target": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "link.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)

		// Point the template source at a symlink.
		writeTemplateSource(t, templatesDir, "real.tpl", "content")
		err := os.Symlink("real.tpl", filepath.Join(templatesDir, "link.tpl"))
		if err != nil {
			t.Fatalf("Failed creating symlink template source: %v", err)
		}

		err = d.templateApplyNow(instance.TemplateTriggerCreate)
		if err == nil {
			t.Fatalf("Expected error for symlink template source")
		}

		_, err = os.Stat(filepath.Join(rootfsDir, "etc", "target"))
		if !os.IsNotExist(err) {
			t.Fatalf("Expected no target file to be created, got err: %v", err)
		}
	})

	t.Run("confines path escape via ..", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"../escape": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "escape.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "escape.tpl", "content")

		err := d.templateApplyNow(instance.TemplateTriggerCreate)
		if err == nil {
			t.Fatalf("Expected error for path escaping rootfs")
		}

		// The file must not be created outside the rootfs (i.e. in the instance dir).
		escaped := filepath.Join(filepath.Dir(rootfsDir), "escape")
		_, err = os.Stat(escaped)
		if !os.IsNotExist(err) {
			t.Fatalf("Expected no file to be written outside rootfs, got err: %v", err)
		}
	})

	t.Run("rejects root target path", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "root.tpl",
				},
			},
		}

		d, _, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "root.tpl", "content")

		err := d.templateApplyNow(instance.TemplateTriggerCreate)
		if err == nil {
			t.Fatalf("Expected error for invalid root target path")
		}
	})

	t.Run("rejects directory target", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "dir.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "dir.tpl", "content")

		// Pre-create the target as a directory.
		err := os.MkdirAll(filepath.Join(rootfsDir, "etc"), 0755)
		if err != nil {
			t.Fatalf("Failed creating target directory: %v", err)
		}

		err = d.templateApplyNow(instance.TemplateTriggerCreate)
		if err == nil {
			t.Fatalf("Expected error for directory target")
		}
	})

	t.Run("rejects target path with symlink escaping rootfs", func(t *testing.T) {
		meta := api.ImageMetadata{
			Templates: map[string]*api.ImageMetadataTemplate{
				"/etc/escape_link": {
					When:     []string{string(instance.TemplateTriggerCreate)},
					Template: "escape_link.tpl",
				},
			},
		}

		d, rootfsDir, templatesDir := newTemplateTestInstance(t, meta)
		writeTemplateSource(t, templatesDir, "escape_link.tpl", "content")

		// Create a symlink at the target that points outside the rootfs.
		err := os.MkdirAll(filepath.Join(rootfsDir, "etc"), 0755)
		if err != nil {
			t.Fatalf("Failed creating etc directory: %v", err)
		}

		targetLink := filepath.Join(rootfsDir, "etc", "escape_link")
		outsideTarget := filepath.Join(filepath.Dir(rootfsDir), "outside_target")
		err = os.Symlink(outsideTarget, targetLink)
		if err != nil {
			t.Fatalf("Failed creating symlink: %v", err)
		}

		err = d.templateApplyNow(instance.TemplateTriggerCreate)
		if err == nil {
			t.Fatalf("Expected error for target path with symlink escaping rootfs")
		}

		// Verify file was not created outside the rootfs.
		_, err = os.Stat(outsideTarget)
		if !os.IsNotExist(err) {
			t.Fatalf("Expected no file to be written outside rootfs via symlink, got err: %v", err)
		}
	})
}
