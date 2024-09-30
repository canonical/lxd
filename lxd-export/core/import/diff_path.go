package importer

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var diffPathWithTags = regexp.MustCompile(`(?m)([\w]+)\((.*)\)`)
var diffTags = regexp.MustCompile(`(?m)([^;\s]+)=([^;\s]+)`)

func hasDiffTags(pathElt string) bool {
	return diffPathWithTags.MatchString(pathElt)
}

func extractStructuredDiffPathElt(pathElt string) (prefix string, tags map[string]string, err error) {
	if !hasDiffTags(pathElt) {
		return pathElt, nil, nil
	}

	parts := diffPathWithTags.FindStringSubmatch(pathElt)
	if len(parts) != 3 {
		return "", nil, fmt.Errorf("Failed to extract structured diff path from %q", pathElt)
	}

	prefix = parts[0]
	tags = make(map[string]string)
	tagParts := diffTags.FindAllStringSubmatch(parts[1], -1)
	for _, tagPart := range tagParts {
		tags[tagPart[0]] = tagPart[1]
	}

	return prefix, tags, nil
}

func isDiffPathCritical(diffPath []string) ([]string, bool, error) {
	// formattedDiffPath is the diff path without the tags
	formattedDiffPath := make([]string, 0)
	for _, pElt := range diffPath {
		prefix, tags, err := extractStructuredDiffPathElt(pElt)
		if err != nil {
			return formattedDiffPath, false, err
		}

		formattedDiffPath = append(formattedDiffPath, prefix)
		if tags != nil {
			severity, ok := tags["severity"]
			if ok && severity == "critical" {
				return formattedDiffPath, true, nil
			}
		}
	}

	return formattedDiffPath, false, nil
}

func isDiffPathWarning(diffPath []string) ([]string, bool, error) {
	formattedDiffPath := make([]string, 0)
	for _, pElt := range diffPath {
		prefix, tags, err := extractStructuredDiffPathElt(pElt)
		if err != nil {
			return formattedDiffPath, false, err
		}

		formattedDiffPath = append(formattedDiffPath, prefix)
		if tags != nil {
			severity, ok := tags["severity"]
			if ok && severity == "warning" {
				return formattedDiffPath, true, nil
			}
		}
	}

	return formattedDiffPath, false, nil
}

func setObjFieldFromDiffVal(obj any, diffFieldToRealField map[string]string, diffFieldPath string, value any) error {
	// First, get the real field path from the diff field path
	fieldPath, ok := diffFieldToRealField[diffFieldPath]
	if !ok {
		return fmt.Errorf("Field %q not found in the real object", diffFieldPath)
	}

	fields := strings.Split(fieldPath, ".")
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("obj must be a non-nil pointer to a struct")
	}

	v = v.Elem() // Dereference ptr
	for i, fieldName := range fields {
		if v.Kind() != reflect.Struct {
			return fmt.Errorf("Field %s is not a struct", strings.Join(fields[:i], "."))
		}

		v = v.FieldByName(fieldName)
		if !v.IsValid() {
			return fmt.Errorf("No such field: %s in obj", strings.Join(fields[:i+1], "."))
		}

		// If not the last field, continue traversing
		if i < len(fields)-1 {
			if v.Kind() == reflect.Ptr {
				if v.IsNil() {
					v.Set(reflect.New(v.Type().Elem()))
				}

				v = v.Elem()
			}
		}
	}

	if !v.CanSet() {
		return fmt.Errorf("Cannot set field %s", fieldPath)
	}

	val := reflect.ValueOf(value)
	if val.Type() != v.Type() {
		if val.Type().ConvertibleTo(v.Type()) {
			val = val.Convert(v.Type())
		} else {
			return fmt.Errorf("Provided value type (%s) did not match field type (%s)", val.Type(), v.Type())
		}
	}

	v.Set(val)
	return nil
}
