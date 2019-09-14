package simplestreams

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

var urlDefaultOS = map[string]string{
	"https://cloud-images.ubuntu.com": "ubuntu",
}

// DownloadableFile represents a file with its URL, hash and size
type DownloadableFile struct {
	Path   string
	Sha256 string
	Size   int64
}

// NewClient returns a simplestreams client for the provided stream URL
func NewClient(url string, httpClient http.Client, useragent string) *SimpleStreams {
	return &SimpleStreams{
		http:           &httpClient,
		url:            url,
		cachedProducts: map[string]*Products{},
		useragent:      useragent,
	}
}

// SimpleStreams represents a simplestream client
type SimpleStreams struct {
	http      *http.Client
	url       string
	useragent string

	cachedStream   *Stream
	cachedProducts map[string]*Products
	cachedImages   []api.Image
	cachedAliases  map[string]map[string]*api.ImageAliasesEntry
}

func (s *SimpleStreams) parseStream() (*Stream, error) {
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

func (s *SimpleStreams) parseProducts(path string) (*Products, error) {
	if s.cachedProducts[path] != nil {
		return s.cachedProducts[path], nil
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
	products := Products{}
	err = json.Unmarshal(body, &products)
	if err != nil {
		return nil, err
	}

	s.cachedProducts[path] = &products

	return &products, nil
}

func (s *SimpleStreams) applyAliases(images []api.Image) ([]api.Image, map[string]map[string]*api.ImageAliasesEntry, error) {
	aliases := map[string]map[string]*api.ImageAliasesEntry{}

	// Sort the images so we tag the preferred ones
	sort.Sort(sortedImages(images))

	// Look for the default OS
	defaultOS := ""
	for k, v := range urlDefaultOS {
		if strings.HasPrefix(s.url, k) {
			defaultOS = v
			break
		}
	}

	addAlias := func(imageType string, name string, fingerprint string) *api.ImageAlias {
		if aliases[imageType] == nil {
			aliases[imageType] = map[string]*api.ImageAliasesEntry{}
		}

		if defaultOS != "" {
			name = strings.TrimPrefix(name, fmt.Sprintf("%s/", defaultOS))
		}

		if aliases[imageType][name] != nil {
			return nil
		}

		alias := api.ImageAliasesEntry{}
		alias.Name = name
		alias.Target = fingerprint
		alias.Type = imageType
		aliases[imageType][name] = &alias

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
					alias := addAlias(image.Type, fmt.Sprintf("%s", entry.Name), image.Fingerprint)
					if alias != nil {
						image.Aliases = append(image.Aliases, *alias)
					}
				}

				// Medium
				alias := addAlias(image.Type, fmt.Sprintf("%s/%s", entry.Name, image.Properties["architecture"]), image.Fingerprint)
				if alias != nil {
					image.Aliases = append(image.Aliases, *alias)
				}
			}
		}

		newImages = append(newImages, image)
	}

	return newImages, aliases, nil
}

func (s *SimpleStreams) getImages() ([]api.Image, map[string]map[string]*api.ImageAliasesEntry, error) {
	if s.cachedImages != nil && s.cachedAliases != nil {
		return s.cachedImages, s.cachedAliases, nil
	}

	images := []api.Image{}

	// Load the stream data
	stream, err := s.parseStream()
	if err != nil {
		return nil, nil, err
	}

	// Iterate through the various indices
	for _, entry := range stream.Index {
		// We only care about images
		if entry.DataType != "image-downloads" {
			continue
		}

		// No point downloading an empty image list
		if len(entry.Products) == 0 {
			continue
		}

		products, err := s.parseProducts(entry.Path)
		if err != nil {
			return nil, nil, err
		}

		streamImages, _ := products.ToLXD()

		for _, image := range streamImages {
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

// GetFiles returns a map of files for the provided image fingerprint
func (s *SimpleStreams) GetFiles(fingerprint string) (map[string]DownloadableFile, error) {
	// Load the main stream
	stream, err := s.parseStream()
	if err != nil {
		return nil, err
	}

	// Iterate through the various indices
	for _, entry := range stream.Index {
		// We only care about images
		if entry.DataType != "image-downloads" {
			continue
		}

		// No point downloading an empty image list
		if len(entry.Products) == 0 {
			continue
		}

		products, err := s.parseProducts(entry.Path)
		if err != nil {
			return nil, err
		}

		images, downloads := products.ToLXD()

		for _, image := range images {
			if strings.HasPrefix(image.Fingerprint, fingerprint) {
				files := map[string]DownloadableFile{}

				for _, path := range downloads[image.Fingerprint] {
					size, err := strconv.ParseInt(path[3], 10, 64)
					if err != nil {
						return nil, err
					}

					files[path[2]] = DownloadableFile{
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

// ListAliases returns a list of image aliases for the provided image fingerprint
func (s *SimpleStreams) ListAliases() ([]api.ImageAliasesEntry, error) {
	_, aliasesMap, err := s.getImages()
	if err != nil {
		return nil, err
	}

	aliases := []api.ImageAliasesEntry{}

	for _, entries := range aliasesMap {
		for _, alias := range entries {
			aliases = append(aliases, *alias)
		}
	}

	return aliases, nil
}

// ListImages returns a list of LXD images
func (s *SimpleStreams) ListImages() ([]api.Image, error) {
	images, _, err := s.getImages()
	return images, err
}

// GetAlias returns a LXD ImageAliasesEntry for the provided alias name
func (s *SimpleStreams) GetAlias(imageType string, name string) (*api.ImageAliasesEntry, error) {
	_, aliasesMap, err := s.getImages()
	if err != nil {
		return nil, err
	}

	var match *api.ImageAliasesEntry
	for entryType, entries := range aliasesMap {
		for aliasName, alias := range entries {
			if aliasName == name && (entryType == imageType || imageType == "") {
				if match != nil {
					return nil, fmt.Errorf("More than one match for alias '%s'", name)
				}

				match = alias
			}
		}
	}

	if match == nil {
		return nil, fmt.Errorf("Alias '%s' doesn't exist", name)
	}

	return match, nil
}

// GetImage returns a LXD image for the provided image fingerprint
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
