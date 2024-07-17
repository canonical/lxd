package apparmor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

var featureCheckProfileTpl = template.Must(template.New("featureCheckProfile").Parse(`
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {

{{- if eq .feature "mount_nosymfollow" }}
  mount options=(nosymfollow) /,
{{- end }}

{{- if eq .feature "userns_rule" }}
  userns,
{{- end }}

}
`))

// FeatureCheck tries to generate feature check profile and process it with apparmor_parser.
func FeatureCheck(sysOS *sys.OS, feature string) (bool, error) {
	randomUUID := uuid.New().String()
	name := fmt.Sprintf("<%s-%s>", randomUUID, feature)
	profileName := profileName("featurecheck", name)
	profilePath := filepath.Join(aaPath, "profiles", profileName)
	content, err := os.ReadFile(profilePath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	updated, err := featureCheckProfile(profileName, feature)
	if err != nil {
		return false, err
	}

	if string(content) != string(updated) {
		err = os.WriteFile(profilePath, []byte(updated), 0600)
		if err != nil {
			return false, err
		}
	}

	defer func() {
		_ = deleteProfile(sysOS, profileName, profileName)
	}()

	err = parseProfile(sysOS, profileName)
	if err != nil {
		return false, nil
	}

	return true, nil
}

// featureCheckProfile generates the AppArmor profile.
func featureCheckProfile(profileName string, feature string) (string, error) {
	// Render the profile.
	sb := &strings.Builder{}
	err := featureCheckProfileTpl.Execute(sb, map[string]any{
		"name":    profileName,
		"snap":    shared.InSnap(),
		"feature": feature,
	})
	if err != nil {
		return "", err
	}

	return sb.String(), nil
}
