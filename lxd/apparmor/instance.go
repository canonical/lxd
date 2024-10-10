package apparmor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/cgroup"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Internal copy of the instance interface.
type instance interface {
	Project() api.Project
	Name() string
	ExpandedConfig() map[string]string
	Type() instancetype.Type
	LogPath() string
	Path() string
	DevicesPath() string
}

type instanceVM interface {
	instance

	FirmwarePath() string
}

// InstanceProfileName returns the instance's AppArmor profile name.
func InstanceProfileName(inst instance) string {
	path := shared.VarPath("")
	name := fmt.Sprintf("%s_<%s>", project.Instance(inst.Project().Name, inst.Name()), path)
	return profileName("", name)
}

// InstanceNamespaceName returns the instance's AppArmor namespace.
func InstanceNamespaceName(inst instance) string {
	// Unlike in profile names, / isn't an allowed character so replace with a -.
	path := strings.Replace(strings.Trim(shared.VarPath(""), "/"), "/", "-", -1)
	name := fmt.Sprintf("%s_<%s>", project.Instance(inst.Project().Name, inst.Name()), path)
	return profileName("", name)
}

// instanceProfileFilename returns the name of the on-disk profile name.
func instanceProfileFilename(inst instance) string {
	name := project.Instance(inst.Project().Name, inst.Name())
	return profileName("", name)
}

// InstanceLoad ensures that the instances's policy is loaded into the kernel so the it can boot.
func InstanceLoad(sysOS *sys.OS, inst instance) error {
	if inst.Type() == instancetype.Container {
		err := createNamespace(sysOS, InstanceNamespaceName(inst))
		if err != nil {
			return err
		}
	}

	err := instanceProfileGenerate(sysOS, inst)
	if err != nil {
		return err
	}

	err = loadProfile(sysOS, instanceProfileFilename(inst))
	if err != nil {
		return err
	}

	return nil
}

// InstanceUnload ensures that the instances's policy namespace is unloaded to free kernel memory.
// This does not delete the policy from disk or cache.
func InstanceUnload(sysOS *sys.OS, inst instance) error {
	if inst.Type() == instancetype.Container {
		err := deleteNamespace(sysOS, InstanceNamespaceName(inst))
		if err != nil {
			return err
		}
	}

	err := unloadProfile(sysOS, InstanceProfileName(inst), instanceProfileFilename(inst))
	if err != nil {
		return err
	}

	return nil
}

// InstanceValidate generates the instance profile file and validates it.
func InstanceValidate(sysOS *sys.OS, inst instance) error {
	err := instanceProfileGenerate(sysOS, inst)
	if err != nil {
		return err
	}

	return parseProfile(sysOS, instanceProfileFilename(inst))
}

// InstanceDelete removes the policy from cache/disk.
func InstanceDelete(sysOS *sys.OS, inst instance) error {
	return deleteProfile(sysOS, InstanceProfileName(inst), instanceProfileFilename(inst))
}

// instanceProfileGenerate generates instance apparmor profile policy file.
func instanceProfileGenerate(sysOS *sys.OS, inst instance) error {
	/* In order to avoid forcing a profile parse (potentially slow) on
	 * every container start, let's use AppArmor's binary policy cache,
	 * which checks mtime of the files to figure out if the policy needs to
	 * be regenerated.
	 *
	 * Since it uses mtimes, we shouldn't just always write out our local
	 * AppArmor template; instead we should check to see whether the
	 * template is the same as ours. If it isn't we should write our
	 * version out so that the new changes are reflected and we definitely
	 * force a recompile.
	 */
	profile := filepath.Join(aaPath, "profiles", instanceProfileFilename(inst))
	content, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	updated, err := instanceProfile(sysOS, inst)
	if err != nil {
		return err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profile, []byte(updated), 0600)
		if err != nil {
			return err
		}
	}

	return nil
}

// instanceProfile generates the AppArmor profile template from the given instance.
func instanceProfile(sysOS *sys.OS, inst instance) (string, error) {
	// Prepare raw.apparmor.
	rawContent := ""
	rawApparmor, ok := inst.ExpandedConfig()["raw.apparmor"]
	if ok {
		for _, line := range strings.Split(strings.Trim(rawApparmor, "\n"), "\n") {
			rawContent += fmt.Sprintf("  %s\n", line)
		}
	}

	// Check for features.
	unixSupported, err := parserSupports(sysOS, "unix")
	if err != nil {
		return "", err
	}

	// Render the profile.
	sb := &strings.Builder{}
	if inst.Type() == instancetype.Container {
		mountNosymfollowSupported, err := parserSupports(sysOS, "mount_nosymfollow")
		if err != nil {
			return "", err
		}

		usernsRuleSupported, err := parserSupports(sysOS, "userns_rule")
		if err != nil {
			return "", err
		}

		err = lxcProfileTpl.Execute(sb, map[string]any{
			"feature_cgns":              sysOS.CGInfo.Namespacing,
			"feature_cgroup2":           sysOS.CGInfo.Layout == cgroup.CgroupsUnified || sysOS.CGInfo.Layout == cgroup.CgroupsHybrid,
			"feature_stacking":          sysOS.AppArmorStacking && !sysOS.AppArmorStacked,
			"feature_unix":              unixSupported,
			"kernel_binfmt":             shared.IsFalseOrEmpty(inst.ExpandedConfig()["security.privileged"]) && sysOS.UnprivBinfmt,
			"feature_mount_nosymfollow": mountNosymfollowSupported,
			"feature_userns_rule":       usernsRuleSupported,
			"name":                      InstanceProfileName(inst),
			"namespace":                 InstanceNamespaceName(inst),
			"nesting":                   shared.IsTrue(inst.ExpandedConfig()["security.nesting"]),
			"raw":                       rawContent,
			"unprivileged":              shared.IsFalseOrEmpty(inst.ExpandedConfig()["security.privileged"]) || sysOS.RunningInUserNS,
		})
		if err != nil {
			return "", err
		}
	} else {
		rootPath := ""
		if shared.InSnap() {
			rootPath = "/var/lib/snapd/hostfs"
		}

		// AppArmor requires deref of all paths.
		path, err := filepath.EvalSymlinks(inst.Path())
		if err != nil {
			return "", err
		}

		vmInst, ok := inst.(instanceVM)
		if !ok {
			return "", fmt.Errorf("Instance is not VM type")
		}

		// Get start time firmware path to allow access to it.
		firmwarePath := vmInst.FirmwarePath()
		if firmwarePath != "" {
			firmwarePath, err = filepath.EvalSymlinks(firmwarePath)
			if err != nil {
				return "", fmt.Errorf("Failed finding firmware: %w", err)
			}
		}

		err = qemuProfileTpl.Execute(sb, map[string]any{
			"devicesPath":       inst.DevicesPath(),
			"libraryPath":       strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":"),
			"logPath":           inst.LogPath(),
			"name":              InstanceProfileName(inst),
			"path":              path,
			"raw":               rawContent,
			"rootPath":          rootPath,
			"snap":              shared.InSnap(),
			"userns":            sysOS.RunningInUserNS,
			"firmwarePath":      firmwarePath,
			"snapExtQemuPrefix": os.Getenv("SNAP_QEMU_PREFIX"),
		})
		if err != nil {
			return "", err
		}
	}

	return sb.String(), nil
}
