package scriptlet

import (
	"fmt"
	"reflect"
	"strings"

	"go.starlark.net/starlark"
)

// StarlarkMarshal converts input to a starlark Value.
// It only includes exported struct fields, and uses the "json" tag for field names.
func StarlarkMarshal(input any) (starlark.Value, error) {
	if input == nil {
		return starlark.None, nil
	}

	sv, ok := input.(starlark.Value)
	if ok {
		return sv, nil
	}

	var err error

	v := reflect.ValueOf(input)

	switch v.Type().Kind() {
	case reflect.String:
		sv = starlark.String(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		sv = starlark.MakeInt(int(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sv = starlark.MakeUint(uint(v.Uint()))
	case reflect.Float32, reflect.Float64:
		sv = starlark.Float(v.Float())
	case reflect.Bool:
		sv = starlark.Bool(v.Bool())
	case reflect.Array, reflect.Slice:
		vlen := v.Len()
		listElems := make([]starlark.Value, 0, vlen)

		for i := 0; i < vlen; i++ {
			lv, err := StarlarkMarshal(v.Index(i).Interface())
			if err != nil {
				return nil, err
			}

			listElems = append(listElems, lv)
		}

		sv = starlark.NewList(listElems)
	case reflect.Map:
		mKeys := v.MapKeys()
		d := starlark.NewDict(len(mKeys))

		for _, k := range v.MapKeys() {
			mv := v.MapIndex(k)
			dv, err := StarlarkMarshal(mv.Interface())
			if err != nil {
				return nil, err
			}

			err = d.SetKey(starlark.String(k.String()), dv)
			if err != nil {
				return nil, fmt.Errorf("Failed setting map key %q to %v: %w", k.String(), dv, err)
			}
		}

		sv = d
	case reflect.Struct:
		fieldCount := v.Type().NumField()
		d := starlark.NewDict(fieldCount)

		for i := 0; i < fieldCount; i++ {
			field := v.Type().Field(i)
			fieldValue := v.Field(i)

			if !field.IsExported() {
				continue
			}

			if field.Anonymous {
				for i := 0; i < fieldValue.Type().NumField(); i++ {
					anonField := fieldValue.Type().Field(i)
					anonFieldValue := fieldValue.Field(i)

					key, _, _ := strings.Cut(anonField.Tag.Get("json"), ",")
					if key == "" {
						key = anonField.Name
					}

					if !anonField.IsExported() {
						continue
					}

					dv, err := StarlarkMarshal(anonFieldValue.Interface())
					if err != nil {
						return nil, err
					}

					err = d.SetKey(starlark.String(key), dv)
					if err != nil {
						return nil, fmt.Errorf("Failed setting struct field %q to %v: %w", key, dv, err)
					}
				}
			} else {
				dv, err := StarlarkMarshal(fieldValue.Interface())
				if err != nil {
					return nil, err
				}

				key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if key == "" {
					key = field.Name
				}

				err = d.SetKey(starlark.String(key), dv)
				if err != nil {
					return nil, fmt.Errorf("Failed setting struct field %q to %v: %w", key, dv, err)
				}
			}
		}

		sv = d
	case reflect.Pointer:
		if v.IsZero() {
			sv = starlark.None
		} else {
			sv, err = StarlarkMarshal(v.Elem().Interface())
			if err != nil {
				return nil, err
			}
		}
	}

	if sv == nil {
		return nil, fmt.Errorf("Unrecognised type %v for value %+v", v.Type(), v.Interface())
	}

	return sv, nil
}
