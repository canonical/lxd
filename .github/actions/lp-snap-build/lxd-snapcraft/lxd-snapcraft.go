package main

import (
	"flag"
	"fmt"
	"log"
	"os"

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

	flag.Parse()

	snapcraftConfig, err := loadSnapcraftYaml(*flagFilePath)
	if err != nil {
		log.Fatal(err)
	}

	if *flagPackageName == "" {
		log.Fatal("Package name is required")
	}

	pkgVersion, pkgConfig := getVersionInfo(*flagPackageName, snapcraftConfig)

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

func loadSnapcraftYaml(snapcraftYamlPath string) (map[string]any, error) {
	buf, err := os.ReadFile(snapcraftYamlPath)
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

func getVersionInfo(pkgName string, snapcraftConfig map[string]any) (string, map[string]any) {
	var pkgVersion string
	var pkgConfig map[string]any

	for k, v := range snapcraftConfig {
		if k == "version" {
			pkgVersion = v.(string)
		} else if k == "parts" {
			for k, v := range v.(map[string]any) {
				if k != pkgName {
					continue
				}

				pkgConfig = v.(map[string]any)
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

	err = os.WriteFile(snapcraftYamlPath, out, 0)
	if err != nil {
		return err
	}

	return nil
}
