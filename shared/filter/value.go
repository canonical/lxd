package filter

import (
	"reflect"
	"strings"
)

// ValueOf returns the value of the given field.
func ValueOf(obj any, field string) any {
	value := reflect.ValueOf(obj)
	valueKind := value.Kind()

	if valueKind == reflect.Pointer {
		if value.IsNil() {
			return nil
		}

		value = value.Elem()
		valueKind = value.Kind()
	}

	key, rest, found := strings.Cut(field, ".")

	if valueKind == reflect.Map {
		switch reflect.TypeOf(obj).Elem().Kind() {
		case reflect.String:
			m, ok := value.Interface().(map[string]string)
			if !ok {
				return nil
			}

			return m[field]
		case reflect.Map:
			for _, entry := range value.MapKeys() {
				if entry.Interface() != key {
					continue
				}

				m := value.MapIndex(entry)
				return ValueOf(m.Interface(), rest)
			}
		}
		return nil
	}

	if valueKind != reflect.Struct {
		return nil
	}

	typ := value.Type()
	var parent any

	for i := range value.NumField() {
		fieldValue := value.Field(i)
		fieldType := typ.Field(i)
		yaml := fieldType.Tag.Get("yaml")

		if yaml == ",inline" {
			parent = fieldValue.Interface()
		}

		yamlKey, _, _ := strings.Cut(yaml, ",")
		if yamlKey == key {
			v := fieldValue.Interface()
			if !found {
				return v
			}

			return ValueOf(v, rest)
		}
	}

	if parent != nil {
		return ValueOf(parent, field)
	}

	return nil
}
