package simplestreams

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
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
	cachedAliases  []extendedAlias

	cachePath   string
	cacheExpiry time.Duration
}

// SetCache configures the on-disk cache
func (s *SimpleStreams) SetCache(path string, expiry time.Duration) {
	s.cachePath = path
	s.cacheExpiry = expiry
}

func (s *SimpleStreams) readCache(path string) ([]byte, bool) {
	cacheName := filepath.Join(s.cachePath, path)

	if s.cachePath == "" {
		return nil, false
	}

	if !shared.PathExists(cacheName) {
		return nil, false
	}

	fi, err := os.Stat(cacheName)
	if err != nil {
		os.Remove(cacheName)
		return nil, false
	}

	body, err := ioutil.ReadFile(cacheName)
	if err != nil {
		os.Remove(cacheName)
		return nil, false
	}

	expired := time.Since(fi.ModTime()) > s.cacheExpiry

	return body, expired
}

func (s *SimpleStreams) cachedDownload(path string) ([]byte, error) {
	fields := strings.Split(path, "/")
	fileName := fields[len(fields)-1]

	// Attempt to get from the cache
	cachedBody, expired := s.readCache(fileName)
	if cachedBody != nil && !expired {
		return cachedBody, nil
	}

	// Download from the source
	uri := fmt.Sprintf("%s/%s", s.url, path)
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	if s.useragent != "" {
		req.Header.Set("User-Agent", s.useragent)
	}

	r, err := s.http.Do(req)
	if err != nil {
		// On local connectivity error, return from cache anyway
		if cachedBody != nil {
			return cachedBody, nil
		}

		return nil, err
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		// On local connectivity error, return from cache anyway
		if cachedBody != nil {
			return cachedBody, nil
		}

		return nil, fmt.Errorf("Unable to fetch %s: %s", uri, r.Status)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Attempt to store in cache
	if s.cachePath != "" {
		cacheName := filepath.Join(s.cachePath, fileName)
		os.Remove(cacheName)
		ioutil.WriteFile(cacheName, body, 0644)
	}

	return body, nil
}

func (s *SimpleStreams) parseStream() (*Stream, error) {
	if s.cachedStream != nil {
		return s.cachedStream, nil
	}

	body, err := s.cachedDownload("streams/v1/index.json")
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

	body, err := s.cachedDownload(path)
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

type extendedAlias struct {
	Name         string
	Alias        *api.ImageAliasesEntry
	Type         string
	Architecture string
}

func (s *SimpleStreams) applyAliases(images []api.Image) ([]api.Image, []extendedAlias, error) {
	aliasesList := []extendedAlias{}

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

	addAlias := func(imageType string, architecture string, name string, fingerprint string) *api.ImageAlias {
		if defaultOS != "" {
			name = strings.TrimPrefix(name, fmt.Sprintf("%s/", defaultOS))
		}

		for _, entry := range aliasesList {
			if entry.Name == name && entry.Type == imageType && entry.Architecture == architecture {
				return nil
			}
		}

		alias := api.ImageAliasesEntry{}
		alias.Name = name
		alias.Target = fingerprint
		alias.Type = imageType

		entry := extendedAlias{
			Name:         name,
			Type:         imageType,
			Alias:        &alias,
			Architecture: architecture,
		}

		aliasesList = append(aliasesList, entry)

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
				alias := addAlias(image.Type, image.Architecture, entry.Name, image.Fingerprint)
				if alias != nil && architectureName == image.Architecture {
					image.Aliases = append(image.Aliases, *alias)
				}

				// Medium
				alias = addAlias(image.Type, image.Architecture, fmt.Sprintf("%s/%s", entry.Name, image.Properties["architecture"]), image.Fingerprint)
				if alias != nil {
					image.Aliases = append(image.Aliases, *alias)
				}
			}
		}

		newImages = append(newImages, image)
	}

	return newImages, aliasesList, nil
}

func (s *SimpleStreams) getImages() ([]api.Image, []extendedAlias, error) {
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
		images = append(images, streamImages...)
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
	_, aliasesList, err := s.getImages()
	if err != nil {
		return nil, err
	}

	// Sort the list ahead of dedup
	sort.Sort(sortedAliases(aliasesList))

	aliases := []api.ImageAliasesEntry{}
	for _, entry := range aliasesList {
		dup := false
		for _, v := range aliases {
			if v.Name == entry.Name && v.Type == entry.Type {
				dup = true
			}
		}

		if dup {
			continue
		}

		aliases = append(aliases, *entry.Alias)
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
	_, aliasesList, err := s.getImages()
	if err != nil {
		return nil, err
	}

	// Sort the list ahead of dedup
	sort.Sort(sortedAliases(aliasesList))

	var match *api.ImageAliasesEntry
	for _, entry := range aliasesList {
		if entry.Name != name {
			continue
		}

		if entry.Type != imageType && imageType != "" {
			continue
		}

		if match != nil {
			if match.Type != entry.Type {
				return nil, fmt.Errorf("More than one match for alias '%s'", name)
			}

			continue
		}

		match = entry.Alias
	}

	if match == nil {
		return nil, fmt.Errorf("Alias '%s' doesn't exist", name)
	}

	return match, nil
}

// GetAliasArchitectures returns a map of architecture / alias entries for an alias
func (s *SimpleStreams) GetAliasArchitectures(imageType string, name string) (map[string]*api.ImageAliasesEntry, error) {
	aliases := map[string]*api.ImageAliasesEntry{}

	_, aliasesList, err := s.getImages()
	if err != nil {
		return nil, err
	}

	for _, entry := range aliasesList {
		if entry.Name != name {
			continue
		}

		if entry.Type != imageType && imageType != "" {
			continue
		}

		if aliases[entry.Architecture] != nil {
			return nil, fmt.Errorf("More than one match for alias '%s'", name)
		}

		aliases[entry.Architecture] = entry.Alias
	}

	if len(aliases) == 0 {
		return nil, fmt.Errorf("Alias '%s' doesn't exist", name)
	}

	return aliases, nil
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
