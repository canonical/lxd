package scriptlet

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"go.starlark.net/starlark"
)

// StarlarkMarshal converts input to a starlark Value.
// It only includes exported struct fields, and uses the "json" tag for field names.
func StarlarkMarshal(input any) (starlark.Value, error) {
	return starlarkMarshal(input, nil)
}

// starlarkMarshal converts input to a starlark Value.
// It only includes exported struct fields, and uses the "json" tag for field names.
// Takes optional parent Starlark dictionary which will be used to set fields from anonymous (embedded) structs
// in to the parent struct.
func starlarkMarshal(input any, parent *starlark.Dict) (starlark.Value, error) {
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

		for _, k := range mKeys {
			kind := k.Kind()
			if kind != reflect.String {
				return nil, fmt.Errorf("Only string keys are supported, found %s", kind)
			}
		}

		sort.Slice(mKeys, func(i, j int) bool {
			return mKeys[i].String() < mKeys[j].String()
		})

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

		d := parent
		if d == nil {
			d = starlark.NewDict(fieldCount)
		}

		for i := 0; i < fieldCount; i++ {
			field := v.Type().Field(i)
			fieldValue := v.Field(i)

			if !field.IsExported() {
				continue
			}

			if field.Anonymous && fieldValue.Kind() == reflect.Struct {
				// If anonymous struct field's value is another struct then pass the the current
				// starlark dictionary to starlarkMarshal so its fields will be set on the parent.
				_, err = starlarkMarshal(fieldValue.Interface(), d)
				if err != nil {
					return nil, err
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
