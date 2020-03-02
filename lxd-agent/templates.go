package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func templatesApply(path string) ([]string, error) {
	metaName := filepath.Join(path, "metadata.yaml")
	if !shared.PathExists(metaName) {
		return nil, nil
	}

	// Parse the metadata.
	content, err := ioutil.ReadFile(metaName)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to read metadata")
	}

	metadata := new(api.ImageMetadata)
	err = yaml.Unmarshal(content, &metadata)
	if err != nil {
		return nil, errors.Wrap(err, "Could not parse metadata.yaml")
	}

	// Go through the files and copy them into place.
	files := []string{}
	for tplPath, tpl := range metadata.Templates {
		filePath := filepath.Join(path, fmt.Sprintf("%s.out", tpl.Template))

		if !shared.PathExists(filePath) {
			continue
		}

		var w *os.File
		if shared.PathExists(tplPath) {
			if tpl.CreateOnly {
				continue
			}

			// Open the existing file.
			w, err = os.Create(tplPath)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to create template file")
			}
		} else {
			// Create the directories leading to the file.
			os.MkdirAll(filepath.Dir(tplPath), 0755)

			// Create the file itself.
			w, err = os.Create(tplPath)
			if err != nil {
				return nil, err
			}

			// Fix mode.
			w.Chmod(0644)
		}
		defer w.Close()

		// Do the copy.
		src, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		_, err = io.Copy(w, src)
		if err != nil {
			return nil, err
		}

		files = append(files, tplPath)
	}

	return files, nil
}
