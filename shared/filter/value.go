package filter

import (
	"reflect"
	"strings"
)

// ValueOf returns the value of the given field.
func ValueOf(obj any, field string) any {
	value := reflect.ValueOf(obj)

	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}

		value = value.Elem()
	}

	parts := strings.Split(field, ".")

	key := parts[0]
	rest := strings.Join(parts[1:], ".")

	if value.Kind() == reflect.Map {
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
			if len(parts) == 1 {
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
