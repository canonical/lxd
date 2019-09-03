package simplestreams

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/osarch"
)

var urlDefaultOS = map[string]string{
	"https://cloud-images.ubuntu.com": "ubuntu",
}

type SimpleStreamsFile struct {
	Path   string
	Sha256 string
	Size   int64
}

func NewClient(url string, httpClient http.Client, useragent string) *SimpleStreams {
	return &SimpleStreams{
		http:           &httpClient,
		url:            url,
		cachedManifest: map[string]*Products{},
		useragent:      useragent,
	}
}

type SimpleStreams struct {
	http      *http.Client
	url       string
	useragent string

	cachedStream   *Stream
	cachedManifest map[string]*Products
	cachedImages   []api.Image
	cachedAliases  map[string]*api.ImageAliasesEntry
}

func (s *SimpleStreams) parseIndex() (*Stream, error) {
	if s.cachedStream != nil {
		return s.cachedStream, nil
	}

	url := fmt.Sprintf("%s/streams/v1/index.json", s.url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if s.useragent != "" {
		req.Header.Set("User-Agent", s.useragent)
	}

	r, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unable to fetch %s: %s", url, r.Status)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Parse the idnex
	stream := Stream{}
	err = json.Unmarshal(body, &stream)
	if err != nil {
		return nil, err
	}

	s.cachedStream = &stream

	return &stream, nil
}

func (s *SimpleStreams) parseManifest(path string) (*Products, error) {
	if s.cachedManifest[path] != nil {
		return s.cachedManifest[path], nil
	}

	url := fmt.Sprintf("%s/%s", s.url, path)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if s.useragent != "" {
		req.Header.Set("User-Agent", s.useragent)
	}

	r, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unable to fetch %s: %s", url, r.Status)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Parse the idnex
	ssManifest := Products{}
	err = json.Unmarshal(body, &ssManifest)
	if err != nil {
		return nil, err
	}

	s.cachedManifest[path] = &ssManifest

	return &ssManifest, nil
}

func (s *SimpleStreams) applyAliases(images []api.Image) ([]api.Image, map[string]*api.ImageAliasesEntry, error) {
	aliases := map[string]*api.ImageAliasesEntry{}

	sort.Sort(sortedImages(images))

	defaultOS := ""
	for k, v := range urlDefaultOS {
		if strings.HasPrefix(s.url, k) {
			defaultOS = v
			break
		}
	}

	addAlias := func(name string, fingerprint string) *api.ImageAlias {
		if defaultOS != "" {
			name = strings.TrimPrefix(name, fmt.Sprintf("%s/", defaultOS))
		}

		if aliases[name] != nil {
			return nil
		}

		alias := api.ImageAliasesEntry{}
		alias.Name = name
		alias.Target = fingerprint
		aliases[name] = &alias

		return &api.ImageAlias{Name: name}
	}

	architectureName, _ := osarch.ArchitectureGetLocal()

	newImages := []api.Image{}
	for _, image := range images {
		if image.Aliases != nil {
			// Build a new list of aliases from the provided ones
			aliases := image.Aliases
			image.Aliases = nil

			for _, entry := range aliases {
				// Short
				if image.Architecture == architectureName {
					alias := addAlias(fmt.Sprintf("%s", entry.Name), image.Fingerprint)
					if alias != nil {
						image.Aliases = append(image.Aliases, *alias)
					}
				}

				// Medium
				alias := addAlias(fmt.Sprintf("%s/%s", entry.Name, image.Properties["architecture"]), image.Fingerprint)
				if alias != nil {
					image.Aliases = append(image.Aliases, *alias)
				}
			}
		}

		newImages = append(newImages, image)
	}

	return newImages, aliases, nil
}

func (s *SimpleStreams) getImages() ([]api.Image, map[string]*api.ImageAliasesEntry, error) {
	if s.cachedImages != nil && s.cachedAliases != nil {
		return s.cachedImages, s.cachedAliases, nil
	}

	images := []api.Image{}

	// Load the main index
	ssIndex, err := s.parseIndex()
	if err != nil {
		return nil, nil, err
	}

	// Iterate through the various image manifests
	for _, entry := range ssIndex.Index {
		// We only care about images
		if entry.DataType != "image-downloads" {
			continue
		}

		// No point downloading an empty image list
		if len(entry.Products) == 0 {
			continue
		}

		manifest, err := s.parseManifest(entry.Path)
		if err != nil {
			return nil, nil, err
		}

		manifestImages, _ := manifest.ToLXD()

		for _, image := range manifestImages {
			images = append(images, image)
		}
	}

	// Setup the aliases
	images, aliases, err := s.applyAliases(images)
	if err != nil {
		return nil, nil, err
	}

	s.cachedImages = images
	s.cachedAliases = aliases

	return images, aliases, nil
}

func (s *SimpleStreams) GetFiles(fingerprint string) (map[string]SimpleStreamsFile, error) {
	// Load the main index
	ssIndex, err := s.parseIndex()
	if err != nil {
		return nil, err
	}

	// Iterate through the various image manifests
	for _, entry := range ssIndex.Index {
		// We only care about images
		if entry.DataType != "image-downloads" {
			continue
		}

		// No point downloading an empty image list
		if len(entry.Products) == 0 {
			continue
		}

		manifest, err := s.parseManifest(entry.Path)
		if err != nil {
			return nil, err
		}

		manifestImages, downloads := manifest.ToLXD()

		for _, image := range manifestImages {
			if strings.HasPrefix(image.Fingerprint, fingerprint) {
				files := map[string]SimpleStreamsFile{}

				for _, path := range downloads[image.Fingerprint] {
					size, err := strconv.ParseInt(path[3], 10, 64)
					if err != nil {
						return nil, err
					}

					files[path[2]] = SimpleStreamsFile{
						Path:   path[0],
						Sha256: path[1],
						Size:   size}
				}

				return files, nil
			}
		}
	}

	return nil, fmt.Errorf("Couldn't find the requested image")
}

func (s *SimpleStreams) downloadFile(path string, hash string, target string, progress func(int64, int64)) error {
	download := func(url string, hash string, target string) error {
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}

		if s.useragent != "" {
			req.Header.Set("User-Agent", s.useragent)
		}

		r, err := s.http.Do(req)
		if err != nil {
			return err
		}
		defer r.Body.Close()

		if r.StatusCode != http.StatusOK {
			return fmt.Errorf("Unable to fetch %s: %s", url, r.Status)
		}

		body := &ioprogress.ProgressReader{
			ReadCloser: r.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length:  r.ContentLength,
				Handler: progress,
			},
		}

		sha256 := sha256.New()
		_, err = io.Copy(io.MultiWriter(out, sha256), body)
		if err != nil {
			return err
		}

		result := fmt.Sprintf("%x", sha256.Sum(nil))
		if result != hash {
			os.Remove(target)
			return fmt.Errorf("Hash mismatch for %s: %s != %s", path, result, hash)
		}

		return nil
	}

	// Try http first
	if strings.HasPrefix(s.url, "https://") {
		err := download(fmt.Sprintf("http://%s/%s", strings.TrimPrefix(s.url, "https://"), path), hash, target)
		if err == nil {
			return nil
		}
	}

	err := download(fmt.Sprintf("%s/%s", s.url, path), hash, target)
	if err != nil {
		return err
	}

	return nil
}

func (s *SimpleStreams) ListAliases() ([]api.ImageAliasesEntry, error) {
	_, aliasesMap, err := s.getImages()
	if err != nil {
		return nil, err
	}

	aliases := []api.ImageAliasesEntry{}

	for _, alias := range aliasesMap {
		aliases = append(aliases, *alias)
	}

	return aliases, nil
}

func (s *SimpleStreams) ListImages() ([]api.Image, error) {
	images, _, err := s.getImages()
	return images, err
}

func (s *SimpleStreams) GetAlias(name string) (*api.ImageAliasesEntry, error) {
	_, aliasesMap, err := s.getImages()
	if err != nil {
		return nil, err
	}

	alias, ok := aliasesMap[name]
	if !ok {
		return nil, fmt.Errorf("Alias '%s' doesn't exist", name)
	}

	return alias, nil
}

func (s *SimpleStreams) GetImage(fingerprint string) (*api.Image, error) {
	images, _, err := s.getImages()
	if err != nil {
		return nil, err
	}

	matches := []api.Image{}

	for _, image := range images {
		if strings.HasPrefix(image.Fingerprint, fingerprint) {
			matches = append(matches, image)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("The requested image couldn't be found")
	} else if len(matches) > 1 {
		return nil, fmt.Errorf("More than one match for the provided partial fingerprint")
	}

	return &matches[0], nil
}

func (s *SimpleStreams) ExportImage(image string, target string) (string, error) {
	if !shared.IsDir(target) {
		return "", fmt.Errorf("Split images can only be written to a directory")
	}

	files, err := s.GetFiles(image)
	if err != nil {
		return "", err
	}

	for _, file := range files {
		fields := strings.Split(file.Path, "/")
		targetFile := filepath.Join(target, fields[len(fields)-1])

		err := s.downloadFile(file.Path, file.Sha256, targetFile, nil)
		if err != nil {
			return "", err
		}
	}

	return target, nil
}

func (s *SimpleStreams) Download(image string, fileType string, target string, progress func(int64, int64)) error {
	files, err := s.GetFiles(image)
	if err != nil {
		return err
	}

	file, ok := files[fileType]
	if ok {
		return s.downloadFile(file.Path, file.Sha256, target, progress)
	}

	return fmt.Errorf("The file couldn't be found")
}
