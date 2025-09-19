package lxd

import (
	"testing"
)

func Test_UseProject(t *testing.T) {
	oldProject := &ProtocolLXD{clusterTarget: "target", project: "old"}
	server := oldProject.UseProject("new")

	if oldProject.project != "old" {
		t.Errorf("UseProject() mutated project: got %q, want %q", oldProject.project, "old")
	}

	if oldProject.clusterTarget != "target" {
		t.Errorf("UseProject() mutated clusterTarget: got %q, want %q", oldProject.clusterTarget, "target")
	}

	newProject, ok := server.(*ProtocolLXD)
	if !ok {
		t.Fatalf("UseProject() returned %T, expected *ProtocolLXD", server)
	}

	if newProject.project != "new" {
		t.Errorf("UseProject() didn't use project: got %q, want %q", newProject.project, "new")
	}

	if newProject.clusterTarget != "target" {
		t.Errorf("UseProject() didn't copy clusterTarget: got %q, want %q", newProject.clusterTarget, "target")
	}
}

func Test_UseTarget(t *testing.T) {
	oldTarget := &ProtocolLXD{clusterTarget: "old", project: "project"}
	server := oldTarget.UseTarget("new")

	if oldTarget.clusterTarget != "old" {
		t.Errorf("UseTarget() mutated clusterTarget: got %q, want %q", oldTarget.clusterTarget, "old")
	}

	if oldTarget.project != "project" {
		t.Errorf("UseTarget() mutated project: got %q, want %q", oldTarget.project, "project")
	}

	newTarget, ok := server.(*ProtocolLXD)
	if !ok {
		t.Fatalf("UseTarget() returned %T, expected *ProtocolLXD", server)
	}

	if newTarget.clusterTarget != "new" {
		t.Errorf("UseTarget() didn't use clusterTarget: got %q, want %q", newTarget.clusterTarget, "new")
	}

	if newTarget.project != "project" {
		t.Errorf("UseTarget() didn't copy project: got %q, want %q", newTarget.project, "project")
	}
}
