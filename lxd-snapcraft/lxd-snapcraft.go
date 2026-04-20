package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	yaml "go.yaml.in/yaml/v3"
)

// lxd-snapcraft
func main() {
	log.SetFlags(0)
	flagFilePath := flag.String("file", "snapcraft.yaml", "Path to snapcraft.yaml file")
	flagPackageName := flag.String("package", "", "Package name")
	flagGetVersion := flag.Bool("get-version", false, "Get version of package and source commit hash for lxd part")
	flagSetVersion := flag.String("set-version", "", "Set version of package")
	flagSetSourceCommit := flag.String("set-source-commit", "", "Set source-commit hash for lxd part")
	flagVerifySourceCommits := flag.Bool("verify-source-commits", false, "Verify that source-commit hashes match their tag comments for all git parts")

	flag.Parse()

	buf, err := os.ReadFile(*flagFilePath)
	if err != nil {
		log.Fatal(err)
	}

	snapcraftConfig, err := loadSnapcraftYaml(bytes.NewReader(buf))
	if err != nil {
		log.Fatal(err)
	}

	if *flagVerifySourceCommits {
		err = verifySourceCommits(bytes.NewReader(buf), snapcraftConfig)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *flagGetVersion || *flagSetVersion != "" || *flagSetSourceCommit != "" {
		if *flagPackageName == "" {
			log.Fatal("Package name is required")
		}

		pkgVersion, pkgConfig := getVersionInfo(*flagPackageName, snapcraftConfig)
		if pkgConfig == nil {
			log.Fatalf("Package %q not found in %s", *flagPackageName, *flagFilePath)
		}

		if *flagGetVersion {
			fmt.Println(pkgVersion)

			if pkgConfig["source-commit"] != nil {
				fmt.Println(pkgConfig["source-commit"])
			}
		}

		writeOut := false

		if *flagSetVersion != "" {
			snapcraftConfig["version"] = *flagSetVersion
			writeOut = true
		}

		if *flagSetSourceCommit != "" {
			pkgConfig["source-commit"] = *flagSetSourceCommit
			delete(pkgConfig, "source-branch") // Can't use source-branch with source-commit.
			writeOut = true
		}

		if writeOut {
			err = writeSnapcraftYaml(*flagFilePath, snapcraftConfig)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func loadSnapcraftYaml(r io.Reader) (map[string]any, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var data map[string]any

	err = yaml.Unmarshal(buf, &data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// sourceCommitComments parses the snapcraft.yaml from r using the yaml.v3 Node
// tree and returns a map of partName -> inline comment on the source-commit key
// (the text after the leading "# ").
func sourceCommitComments(r io.Reader) (map[string]string, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	err = yaml.Unmarshal(buf, &doc)
	if err != nil {
		return nil, err
	}

	// doc is a DocumentNode; its single child is the root MappingNode.
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("unexpected YAML structure")
	}

	root := doc.Content[0]
	comments := make(map[string]string)

	// Walk the root mapping to find the "parts" key.
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "parts" {
			continue
		}

		partsNode := root.Content[i+1]

		// Each pair in partsNode.Content is (partNameNode, partMappingNode).
		for j := 0; j+1 < len(partsNode.Content); j += 2 {
			partName := partsNode.Content[j].Value
			partMapping := partsNode.Content[j+1]

			// Walk the part's keys to find "source-commit".
			for k := 0; k+1 < len(partMapping.Content); k += 2 {
				if partMapping.Content[k].Value != "source-commit" {
					continue
				}

				comment := strings.TrimPrefix(partMapping.Content[k+1].LineComment, "# ")
				comments[partName] = comment
			}
		}
	}

	return comments, nil
}

func getVersionInfo(pkgName string, snapcraftConfig map[string]any) (string, map[string]any) {
	var pkgVersion string
	var pkgConfig map[string]any

	for k, v := range snapcraftConfig {
		if k == "version" {
			ver, ok := v.(string)
			if ok {
				pkgVersion = ver
			}
		} else if k == "parts" {
			parts, ok := v.(map[string]any)
			if !ok {
				continue
			}

			for k, v := range parts {
				if k != pkgName {
					continue
				}

				partConfig, ok := v.(map[string]any)
				if ok {
					pkgConfig = partConfig
				}
			}
		}
	}

	return pkgVersion, pkgConfig
}

func writeSnapcraftYaml(snapcraftYamlPath string, snapcraftConfig map[string]any) error {
	out, err := yaml.Marshal(snapcraftConfig)
	if err != nil {
		return err
	}

	err = os.WriteFile(snapcraftYamlPath, out, 0644)
	if err != nil {
		return err
	}

	return nil
}

// verifySourceCommits checks that every git part whose source-commit has an
// inline tag comment resolves to the expected SHA via git ls-remote.
func verifySourceCommits(r io.Reader, snapcraftConfig map[string]any) error {
	comments, err := sourceCommitComments(r)
	if err != nil {
		return err
	}

	parts, ok := snapcraftConfig["parts"].(map[string]any)
	if !ok {
		return nil
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		errs   []string
	)

	for partName, partVal := range parts {
		partCfg, ok := partVal.(map[string]any)
		if !ok {
			continue
		}

		if partCfg["source-type"] != "git" {
			continue
		}

		sourceCommit, _ := partCfg["source-commit"].(string)
		if sourceCommit == "" {
			continue
		}

		tag, hasComment := comments[partName]
		if !hasComment || tag == "" {
			fmt.Fprintf(os.Stderr, "warning: part %s: source-commit has no tag comment\n", partName)
			continue
		}

		// A comment starting with "pre " marks a pre-release commit that is not
		// yet associated with a tag; skip verification silently.
		if strings.HasPrefix(tag, "pre ") {
			continue
		}

		source, _ := partCfg["source"].(string)

		wg.Add(1)
		go func(partName, source, tag, sourceCommit string) {
			defer wg.Done()

			resolved, err := lsRemoteTag(source, tag)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("part %s: %v", partName, err))
				mu.Unlock()
				return
			}

			if !strings.EqualFold(resolved, sourceCommit) {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("part %s: source-commit mismatch for tag %s: snapcraft.yaml has %s, remote has %s", partName, tag, sourceCommit, resolved))
				mu.Unlock()
			}
		}(partName, source, tag, sourceCommit)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}

	return nil
}

// lsRemoteTag resolves a git tag at the given remote URL to a commit SHA.
// It prefers the dereferenced SHA (annotated tags) over the direct ref (lightweight tags).
func lsRemoteTag(remote, tag string) (string, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", remote,
		"refs/tags/"+tag, "refs/tags/"+tag+"^{}")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote failed for tag %s: %w", tag, err)
	}

	var direct, deref string

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		sha, ref := fields[0], fields[1]
		if ref == "refs/tags/"+tag+"^{}" {
			deref = sha
		} else if ref == "refs/tags/"+tag {
			direct = sha
		}
	}

	if deref != "" {
		return deref, nil
	}

	if direct != "" {
		return direct, nil
	}

	return "", fmt.Errorf("tag %s not found at remote %s", tag, remote)
}
