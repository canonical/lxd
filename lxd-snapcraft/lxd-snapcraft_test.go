package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoSnapcraftYaml = "../snapcraft.yaml"

func TestLoadSnapcraftYaml(t *testing.T) {
	f, err := os.Open(repoSnapcraftYaml)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	config, err := loadSnapcraftYaml(f)
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	if config["name"] != "lxd" {
		t.Errorf("expected snap name 'lxd', got %v", config["name"])
	}
}

func TestGetVersionInfo(t *testing.T) {
	f, err := os.Open(repoSnapcraftYaml)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	config, err := loadSnapcraftYaml(f)
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	version, partCfg := getVersionInfo("lxd", config)
	if version == "" {
		t.Error("expected non-empty version")
	}

	if partCfg == nil {
		t.Fatal("expected non-nil part config for 'lxd'")
	}

	if partCfg["source-type"] != "git" {
		t.Errorf("expected source-type 'git', got %v", partCfg["source-type"])
	}
}

func TestGetVersionInfo_UnknownPart(t *testing.T) {
	f, err := os.Open(repoSnapcraftYaml)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	config, err := loadSnapcraftYaml(f)
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	_, partCfg := getVersionInfo("nonexistent-part", config)
	if partCfg != nil {
		t.Error("expected nil part config for unknown part")
	}
}

func TestSetVersion(t *testing.T) {
	// Work on a copy to avoid modifying the real file.
	src, err := os.ReadFile(repoSnapcraftYaml)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "snapcraft.yaml")
	err = os.WriteFile(tmpFile, src, 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	config, err := loadSnapcraftYaml(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	config["version"] = "99.99"
	err = writeSnapcraftYaml(tmpFile, config)
	if err != nil {
		t.Fatalf("writeSnapcraftYaml failed: %v", err)
	}

	f, err := os.Open(tmpFile)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	config2, err := loadSnapcraftYaml(f)
	if err != nil {
		t.Fatalf("loadSnapcraftYaml (re-read) failed: %v", err)
	}

	if config2["version"] != "99.99" {
		t.Errorf("expected version '99.99', got %v", config2["version"])
	}
}

func TestSetSourceCommit(t *testing.T) {
	src, err := os.ReadFile(repoSnapcraftYaml)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	tmpFile := filepath.Join(t.TempDir(), "snapcraft.yaml")
	err = os.WriteFile(tmpFile, src, 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	config, err := loadSnapcraftYaml(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	_, partCfg := getVersionInfo("lxd", config)
	if partCfg == nil {
		t.Fatal("expected non-nil part config for 'lxd'")
	}

	newCommit := "abcdef1234567890abcdef1234567890abcdef12"
	partCfg["source-commit"] = newCommit
	delete(partCfg, "source-branch")

	err = writeSnapcraftYaml(tmpFile, config)
	if err != nil {
		t.Fatalf("writeSnapcraftYaml failed: %v", err)
	}

	f, err := os.Open(tmpFile)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	config2, err := loadSnapcraftYaml(f)
	if err != nil {
		t.Fatalf("loadSnapcraftYaml (re-read) failed: %v", err)
	}

	_, partCfg2 := getVersionInfo("lxd", config2)
	if partCfg2["source-commit"] != newCommit {
		t.Errorf("expected source-commit %q, got %v", newCommit, partCfg2["source-commit"])
	}

	if partCfg2["source-branch"] != nil {
		t.Error("expected source-branch to be removed")
	}
}

func TestSourceCommitComments(t *testing.T) {
	f, err := os.Open(repoSnapcraftYaml)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	comments, err := sourceCommitComments(f)
	if err != nil {
		t.Fatalf("sourceCommitComments failed: %v", err)
	}

	// The repo's snapcraft.yaml has several parts with source-commit comments.
	// Check that at least one known part has a non-empty comment.
	found := false
	for _, comment := range comments {
		if comment != "" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected at least one part with a non-empty source-commit comment")
	}
}

func TestSourceCommitComments_NoComment(t *testing.T) {
	f, err := os.Open("testdata/no-comment.yaml")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	defer f.Close()

	comments, err := sourceCommitComments(f)
	if err != nil {
		t.Fatalf("sourceCommitComments failed: %v", err)
	}

	comment := comments["mypart"]
	if comment != "" {
		t.Errorf("expected empty comment for part without inline comment, got %q", comment)
	}
}

func TestVerifySourceCommits_NoComment(t *testing.T) {
	buf, err := os.ReadFile("testdata/no-comment.yaml")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	config, err := loadSnapcraftYaml(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	// Should warn but not error.
	err = verifySourceCommits(bytes.NewReader(buf), config)
	if err != nil {
		t.Fatalf("expected no error for missing comment, got: %v", err)
	}
}

func TestVerifySourceCommits_Mismatch(t *testing.T) {
	buf, err := os.ReadFile("testdata/mismatch.yaml")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	config, err := loadSnapcraftYaml(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	err = verifySourceCommits(bytes.NewReader(buf), config)
	if err == nil {
		t.Fatal("expected error for SHA mismatch")
	}

	if !strings.Contains(err.Error(), "source-commit mismatch") {
		t.Errorf("expected mismatch error message, got: %v", err)
	}
}

func TestVerifySourceCommits_PreComment(t *testing.T) {
	buf, err := os.ReadFile("testdata/pre-comment.yaml")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	config, err := loadSnapcraftYaml(bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("loadSnapcraftYaml failed: %v", err)
	}

	// A "pre <tag>" comment means the commit is not yet tagged; verification
	// should be skipped silently without error or warning.
	err = verifySourceCommits(bytes.NewReader(buf), config)
	if err != nil {
		t.Fatalf("expected no error for pre-release comment, got: %v", err)
	}
}
