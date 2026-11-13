package main

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared/api"
)

type utilsPropertiesTestSuite struct {
	suite.Suite
}

func TestUtilsPropertiesTestSuite(t *testing.T) {
	suite.Run(t, new(utilsPropertiesTestSuite))
}

func (s *utilsPropertiesTestSuite) TestStringToTimeHookFuncValidData() {
	layout := time.RFC3339
	hook := stringToTimeHookFunc(layout)

	result, err := hook(reflect.TypeFor[string](), reflect.TypeFor[time.Time](), "2023-07-12T07:34:00Z")
	s.NoError(err)
	s.Equal(time.Date(2023, 7, 12, 7, 34, 0, 0, time.UTC), result)
}

func (s *utilsPropertiesTestSuite) TestStringToTimeHookFuncInvalidData() {
	layout := time.RFC3339
	hook := stringToTimeHookFunc(layout)

	_, err := hook(reflect.TypeFor[string](), reflect.TypeFor[time.Time](), "not a time")
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestStringToBoolHookFuncValidData() {
	hookFunc := stringToBoolHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error)) //nolint:revive

	result, err := hook(reflect.String, reflect.Bool, "t")
	s.NoError(err)
	s.Equal(true, result)
}

func (s *utilsPropertiesTestSuite) TestStringToBoolHookFuncInvalidData() {
	hookFunc := stringToBoolHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error)) //nolint:revive

	_, err := hook(reflect.String, reflect.Bool, "not a boolean")
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestStringToIntHookFuncValidData() {
	hookFunc := stringToIntHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error)) //nolint:revive

	result, err := hook(reflect.String, reflect.Int, "123")
	s.NoError(err)
	s.Equal(123, result)
}

func (s *utilsPropertiesTestSuite) TestStringToIntHookFuncInvalidData() {
	hookFunc := stringToIntHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error)) //nolint:revive

	_, err := hook(reflect.String, reflect.Int, "not an int")
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestStringToFloatHookFuncValidData() {
	hookFunc := stringToFloatHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error)) //nolint:revive

	result, err := hook(reflect.String, reflect.Float64, "123.45")
	s.NoError(err)
	s.Equal(123.45, result)
}

func (s *utilsPropertiesTestSuite) TestStringToFloatHookFuncInvalidData() {
	hookFunc := stringToFloatHookFunc()
	hook := hookFunc.(func(reflect.Kind, reflect.Kind, any) (any, error)) //nolint:revive

	_, err := hook(reflect.String, reflect.Float64, "not a float")
	s.Error(err, "Expected an error but got nil")
}

type testStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (s *utilsPropertiesTestSuite) TestSetFieldByJsonTagSettable() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	setFieldByJSONTag(&ts, "name", "Jane Doe")
	s.Equal("Jane Doe", ts.Name)
}

func (s *utilsPropertiesTestSuite) TestSetFieldByJsonTagNonSettable() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	setFieldByJSONTag(&ts, "invalid name", "Jane Doe")
	s.NotEqual("Jane Doe", ts.Name)
}

func (s *utilsPropertiesTestSuite) TestUnsetFieldByJsonTagValid() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	err := unsetFieldByJSONTag(&ts, "name")
	s.NoError(err)
	s.Empty(ts.Name)
}

func (s *utilsPropertiesTestSuite) TestUnsetFieldByJsonTagInvalid() {
	ts := testStruct{
		Name: "John Doe",
		Age:  30,
	}

	err := unsetFieldByJSONTag(&ts, "invalid")
	s.Error(err, "Expected an error but got nil")
}

type writableStruct struct {
	Name  string    `json:"name"`
	Age   int       `json:"age"`
	Score float64   `json:"score"`
	Alive bool      `json:"alive"`
	Birth time.Time `json:"birth"`
}

func (s *utilsPropertiesTestSuite) TestUnpackKVToWritable() {
	ws := &writableStruct{}
	keys := map[string]string{
		"name":  "John Doe",
		"age":   "30",
		"score": "85.5",
		"alive": "true",
		"birth": "2000-01-01T00:00:00Z",
	}

	err := unpackKVToWritable(ws, keys)
	s.NoError(err)

	s.Equal("John Doe", ws.Name)
	s.Equal(30, ws.Age)
	s.Equal(85.5, ws.Score)
	s.True(ws.Alive)
	s.Equal("2000-01-01T00:00:00Z", ws.Birth.Format(time.RFC3339))
}

func (s *utilsPropertiesTestSuite) TestUnpackKVToWritableInvalidData() {
	ws := &writableStruct{}
	keys := map[string]string{
		"name":  "John Doe",
		"age":   "not an int",
		"score": "not a float",
		"alive": "not a bool",
		"birth": "not a time",
	}

	err := unpackKVToWritable(ws, keys)
	s.Error(err, "Expected an error but got nil")
}

func (s *utilsPropertiesTestSuite) TestCheckReadOnlyYAMLFields() {
	created := time.Date(2024, 6, 1, 12, 0, 0, 123, time.UTC)
	snap := &api.InstanceSnapshot{
		Name:            "snap0",
		Architecture:    "x86_64",
		Config:          map[string]string{"volatile.uuid": "abc"},
		ExpandedConfig:  map[string]string{"volatile.uuid": "abc"},
		Devices:         map[string]map[string]string{"root": {"path": "/", "pool": "default", "type": "disk"}},
		ExpandedDevices: map[string]map[string]string{"root": {"path": "/", "pool": "default", "type": "disk"}},
		CreatedAt:       created,
		LastUsedAt:      time.Time{},
		ExpiresAt:       time.Time{},
		Profiles:        []string{"default"},
		Size:            4096,
	}

	editable := getEditableYAMLFields(api.InstanceSnapshotPut{})
	msg := "Only " + formatFieldList(editable) + " field(s) can be modified for instance snapshots"
	raw, err := yaml.Marshal(snap)
	s.NoError(err)

	s.NoError(checkReadOnlyYAMLFields(snap, []byte("expires_at: 2100-01-01T00:00:00Z\n"), editable, msg))
	s.NoError(checkReadOnlyYAMLFields(snap, []byte("expires_at:\n"), editable, msg))
	s.NoError(checkReadOnlyYAMLFields(snap, bytes.Replace(raw, []byte("expires_at: 0001-01-01T00:00:00Z"), []byte("expires_at: 2100-01-01T00:00:00Z"), 1), editable, msg))
	s.EqualError(checkReadOnlyYAMLFields(snap, bytes.Replace(raw, []byte("name: snap0"), []byte("name: invalid-name"), 1), editable, msg), msg)
	s.EqualError(checkReadOnlyYAMLFields(snap, bytes.Replace(raw, []byte("ephemeral: false"), []byte("ephemeral: true"), 1), editable, msg), msg)
	s.EqualError(checkReadOnlyYAMLFields(snap, bytes.Replace(raw, []byte("config:\n  volatile.uuid: abc\n"), []byte("config: {}\n"), 1), editable, msg), msg)
	s.EqualError(checkReadOnlyYAMLFields(snap, bytes.Replace(raw, []byte("profiles:\n- default\n"), []byte("profiles: []\n"), 1), editable, msg), msg)
	s.EqualError(checkReadOnlyYAMLFields(snap, bytes.Replace(raw, []byte("devices:\n  root:\n    path: /\n    pool: default\n    type: disk\n"), []byte("devices: {}\n"), 1), editable, msg), msg)

	parseErr := checkReadOnlyYAMLFields(snap, []byte("name: [\n"), editable, msg)
	s.Error(parseErr)
	s.NotEqual(msg, parseErr.Error())

	nilCfg := *snap
	nilCfg.Config = nil
	rawNil, err := yaml.Marshal(&nilCfg)
	s.NoError(err)
	s.NoError(checkReadOnlyYAMLFields(&nilCfg, rawNil, editable, msg))

	expiry := time.Date(2100, 1, 2, 15, 4, 5, 0, time.UTC)
	vol := &api.StorageVolumeSnapshot{
		Name:        "snap0",
		Description: "foo",
		ContentType: "filesystem",
		CreatedAt:   created,
		ExpiresAt:   &expiry,
		Config:      map[string]string{"size": "1MiB", "volatile.uuid": "abc"},
	}

	volEditable := getEditableYAMLFields(api.StorageVolumeSnapshotPut{})
	volMsg := "Only " + formatFieldList(volEditable) + " field(s) can be modified for snapshot volumes"
	volRaw, err := yaml.Marshal(vol)
	s.NoError(err)

	s.NoError(checkReadOnlyYAMLFields(vol, bytes.Replace(volRaw, []byte("description: foo"), []byte("description: bar"), 1), volEditable, volMsg))
	s.EqualError(checkReadOnlyYAMLFields(vol, bytes.Replace(volRaw, []byte("name: snap0"), []byte("name: invalid-name"), 1), volEditable, volMsg), volMsg)
	s.EqualError(checkReadOnlyYAMLFields(vol, bytes.Replace(volRaw, []byte("content_type: filesystem"), []byte("content_type: invalid-content_type"), 1), volEditable, volMsg), volMsg)
	s.EqualError(checkReadOnlyYAMLFields(vol, bytes.Replace(volRaw, []byte("config:\n  size: 1MiB\n  volatile.uuid: abc\n"), []byte("config: {}\n"), 1), volEditable, volMsg), volMsg)

	kept := make([]byte, 0, len(volRaw))
	for _, line := range bytes.Split(volRaw, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("expires_at:")) {
			continue
		}

		kept = append(kept, line...)
		kept = append(kept, '\n')
	}

	s.NoError(checkReadOnlyYAMLFields(vol, kept, volEditable, volMsg))
}
