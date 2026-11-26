package main

import (
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

func TestAuthGroupLegacyImageAliasPermissionRemoval(t *testing.T) {
	server, container, vm, _ := newImageAliasTestServer(t)
	projectView := api.Permission{EntityType: "project", EntityReference: "/1.0/projects/default", Entitlement: "can_view"}

	for _, tt := range []struct {
		imageType   string
		fingerprint string
	}{
		{imageType: "container", fingerprint: container},
		{imageType: "virtual-machine", fingerprint: vm},
	} {
		t.Run(tt.imageType, func(t *testing.T) {
			name := "legacy-" + tt.imageType
			err := server.CreateImageAlias(api.ImageAliasesPost{ImageAliasesEntry: api.ImageAliasesEntry{Name: name, Target: tt.fingerprint}})
			require.NoError(t, err)
			typedPermission := api.Permission{
				EntityType:      "image_alias",
				EntityReference: entity.ImageAliasURL("default", tt.imageType, name).String(),
				Entitlement:     "can_view",
			}

			err = server.CreateAuthGroup(api.AuthGroupsPost{
				AuthGroupPost: api.AuthGroupPost{Name: name},
				AuthGroupPut:  api.AuthGroupPut{Permissions: []api.Permission{projectView, typedPermission}},
			})
			require.NoError(t, err)

			// This is the exact permission constructed by a pre-extension client, without normalization.
			legacyPermission := typedPermission
			legacyPermission.EntityReference = "/1.0/images/aliases/" + name + "?project=default"
			group, etag, err := getLegacyAuthGroup(server, name)
			require.NoError(t, err)
			require.NotEmpty(t, etag)
			require.Contains(t, group.Permissions, legacyPermission)
			groups, err := getLegacyAuthGroups(server)
			require.NoError(t, err)
			index := slices.IndexFunc(groups, func(group api.AuthGroup) bool { return group.Name == name })
			require.NotEqual(t, -1, index)
			require.ElementsMatch(t, group.Permissions, groups[index].Permissions)

			typedGroup, typedETag, err := server.GetAuthGroup(name)
			require.NoError(t, err)
			require.Contains(t, typedGroup.Permissions, typedPermission)
			require.Equal(t, etag, typedETag)
			typedGroups, err := server.GetAuthGroups()
			require.NoError(t, err)
			typedIndex := slices.IndexFunc(typedGroups, func(group api.AuthGroup) bool { return group.Name == name })
			require.NotEqual(t, -1, typedIndex)
			require.ElementsMatch(t, typedGroup.Permissions, typedGroups[typedIndex].Permissions)
			err = server.UpdateAuthGroup(name, typedGroup.Writable(), typedETag)
			require.NoError(t, err)

			// GET and PUT ETag representations must agree, even while an alias grant is present.
			err = server.UpdateAuthGroup(name, group.Writable(), etag)
			require.NoError(t, err)
			group, etag, err = getLegacyAuthGroup(server, name)
			require.NoError(t, err)
			put := group.Writable()
			index = slices.Index(put.Permissions, legacyPermission)
			require.NotEqual(t, -1, index)
			put.Permissions = slices.Delete(put.Permissions, index, index+1)
			err = server.UpdateAuthGroup(name, put, etag)
			require.NoError(t, err)
			group, _, err = getLegacyAuthGroup(server, name)
			require.NoError(t, err)
			require.Equal(t, []api.Permission{projectView}, group.Permissions)

			// Once the name is shared, group responses retain the unambiguous typed identity.
			otherFingerprint := vm
			if tt.imageType == "virtual-machine" {
				otherFingerprint = container
			}

			err = server.CreateImageAlias(api.ImageAliasesPost{ImageAliasesEntry: api.ImageAliasesEntry{Name: name, Target: otherFingerprint}})
			require.NoError(t, err)
			err = server.UpdateAuthGroup(name, api.AuthGroupPut{Permissions: []api.Permission{projectView, typedPermission}}, "")
			require.NoError(t, err)
			group, etag, err = getLegacyAuthGroup(server, name)
			require.NoError(t, err)
			require.Contains(t, group.Permissions, typedPermission)
			require.NotContains(t, group.Permissions, legacyPermission)
			groups, err = getLegacyAuthGroups(server)
			require.NoError(t, err)
			index = slices.IndexFunc(groups, func(group api.AuthGroup) bool { return group.Name == name })
			require.NotEqual(t, -1, index)
			require.ElementsMatch(t, group.Permissions, groups[index].Permissions)
			err = server.UpdateAuthGroup(name, group.Writable(), etag)
			require.NoError(t, err)
		})
	}
}

func TestAuthGroupPatchImageAliasPermissions(t *testing.T) {
	server, container, _, _ := newImageAliasTestServer(t)
	err := server.CreateImageAlias(api.ImageAliasesPost{ImageAliasesEntry: api.ImageAliasesEntry{Name: "legacy", Target: container}})
	require.NoError(t, err)
	projectView := api.Permission{EntityType: "project", EntityReference: "/1.0/projects/default", Entitlement: "can_view"}
	legacyView := api.Permission{EntityType: "image_alias", EntityReference: "/1.0/images/aliases/legacy?project=default", Entitlement: "can_view"}
	typedView := legacyView
	typedView.EntityReference = entity.ImageAliasURL("default", "container", "legacy").String()
	legacyEdit := legacyView
	legacyEdit.Entitlement = "can_edit"
	legacyDelete := legacyView
	legacyDelete.Entitlement = "can_delete"
	typedDelete := typedView
	typedDelete.Entitlement = "can_delete"
	permissions := []api.Permission{projectView, legacyView, legacyEdit}
	err = server.CreateAuthGroup(api.AuthGroupsPost{
		AuthGroupPost: api.AuthGroupPost{Name: "patch-test"},
		AuthGroupPut:  api.AuthGroupPut{Description: "Retain this description", Permissions: permissions},
	})
	require.NoError(t, err)

	for _, tt := range []struct {
		name        string
		permissions []api.Permission
		addDelete   bool
	}{
		{name: "omitted permissions"},
		{name: "empty permissions", permissions: []api.Permission{}},
		{name: "repeated legacy", permissions: []api.Permission{projectView, legacyView}},
		{name: "repeated typed", permissions: []api.Permission{projectView, typedView}},
		{name: "mixed repeated", permissions: []api.Permission{projectView, legacyView, typedView, legacyView}},
		{name: "new entitlement using existing views", permissions: []api.Permission{legacyDelete, typedDelete}, addDelete: true},
		{name: "repeated entitlement", permissions: []api.Permission{legacyDelete, typedDelete}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, etag, err := server.GetAuthGroup("patch-test")
			require.NoError(t, err)
			require.NotEmpty(t, etag)
			body := map[string]any{}
			if tt.permissions != nil {
				body["permissions"] = tt.permissions
			}

			_, _, err = server.RawQuery(http.MethodPatch, "/1.0/auth/groups/patch-test", body, etag)
			require.NoError(t, err)
			if tt.addDelete {
				permissions = append(permissions, legacyDelete)
			}

			group, _, err := getLegacyAuthGroup(server, "patch-test")
			require.NoError(t, err)
			require.Equal(t, "Retain this description", group.Description)
			require.ElementsMatch(t, permissions, group.Permissions)
		})
	}
}

func getLegacyAuthGroup(server lxd.InstanceServer, name string) (*api.AuthGroup, string, error) {
	resp, etag, err := server.RawQuery(http.MethodGet, "/1.0/auth/groups/"+name, nil, "")
	if err != nil {
		return nil, "", err
	}

	var group api.AuthGroup
	err = resp.MetadataAsStruct(&group)
	return &group, etag, err
}

func getLegacyAuthGroups(server lxd.InstanceServer) ([]api.AuthGroup, error) {
	resp, _, err := server.RawQuery(http.MethodGet, "/1.0/auth/groups?recursion=1", nil, "")
	if err != nil {
		return nil, err
	}

	var groups []api.AuthGroup
	err = resp.MetadataAsStruct(&groups)
	return groups, err
}
