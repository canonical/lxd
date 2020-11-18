package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/api"
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
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			filePath := filepath.Join(path, fmt.Sprintf("%s.out", tpl.Template))

			if !shared.PathExists(filePath) {
				return nil
			}

			var w *os.File
			if shared.PathExists(tplPath) {
				if tpl.CreateOnly {
					return nil
				}

				// Open the existing file.
				w, err = os.Create(tplPath)
				if err != nil {
					return errors.Wrap(err, "Failed to create template file")
				}
			} else {
				// Create the directories leading to the file.
				os.MkdirAll(filepath.Dir(tplPath), 0755)

				// Create the file itself.
				w, err = os.Create(tplPath)
				if err != nil {
					return err
				}

				// Fix mode.
				w.Chmod(0644)
			}
			defer w.Close()

			// Do the copy.
			src, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer src.Close()

			_, err = io.Copy(w, src)
			if err != nil {
				return err
			}

			files = append(files, tplPath)

			return nil
		}(tplPath, tpl)

		if err != nil {
			return nil, err
		}
	}

	return files, nil
}
