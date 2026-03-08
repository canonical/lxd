package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared/api"
)

func templatesApply(path string) ([]string, error) {
	metaName := filepath.Join(path, "metadata.yaml")

	// Parse the metadata.
	content, err := os.ReadFile(metaName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("Failed to read metadata: %w", err)
	}

	metadata := new(api.ImageMetadata)
	err = yaml.Unmarshal(content, &metadata)
	if err != nil {
		return nil, fmt.Errorf("Could not parse metadata.yaml: %w", err)
	}

	// Go through the files and copy them into place.
	files := []string{}
	for tplPath, tpl := range metadata.Templates {
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			filePath := filepath.Join(path, tpl.Template+".out")

			// Open the rendered template output.
			src, err := os.Open(filePath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}

				return err
			}

			defer func() { _ = src.Close() }()

			// Ensure parent directories exist.
			err = os.MkdirAll(filepath.Dir(tplPath), 0755)
			if err != nil {
				return err
			}

			var w *os.File
			if tpl.CreateOnly {
				// Only create the file if it doesn't already exist.
				w, err = os.OpenFile(tplPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
				if err != nil {
					if os.IsExist(err) {
						return nil
					}

					return err
				}
			} else {
				w, err = os.OpenFile(tplPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
				if err != nil {
					return fmt.Errorf("Failed to create template file: %w", err)
				}

				err = w.Chmod(0644)
				if err != nil {
					return fmt.Errorf("Failed to set template file permissions: %w", err)
				}
			}

			defer func() { _ = w.Close() }()

			// Do the copy.
			_, err = io.Copy(w, src)
			if err != nil {
				return err
			}

			err = w.Close()
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
