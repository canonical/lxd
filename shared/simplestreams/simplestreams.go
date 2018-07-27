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
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/osarch"
)

type ssSortImage []api.Image

func (a ssSortImage) Len() int {
	return len(a)
}

func (a ssSortImage) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ssSortImage) Less(i, j int) bool {
	if a[i].Properties["os"] == a[j].Properties["os"] {
		if a[i].Properties["release"] == a[j].Properties["release"] {
			if !shared.TimeIsSet(a[i].CreatedAt) {
				return true
			}

			if !shared.TimeIsSet(a[j].CreatedAt) {
				return false
			}

			if a[i].CreatedAt == a[j].CreatedAt {
				return a[i].Properties["serial"] > a[j].Properties["serial"]
			}

			return a[i].CreatedAt.UTC().Unix() > a[j].CreatedAt.UTC().Unix()
		}

		if a[i].Properties["release"] == "" {
			return false
		}

		if a[j].Properties["release"] == "" {
			return true
		}

		return a[i].Properties["release"] < a[j].Properties["release"]
	}

	if a[i].Properties["os"] == "" {
		return false
	}

	if a[j].Properties["os"] == "" {
		return true
	}

	return a[i].Properties["os"] < a[j].Properties["os"]
}

var ssDefaultOS = map[string]string{
	"https://cloud-images.ubuntu.com": "ubuntu",
}

type SimpleStreamsManifest struct {
	Updated  string                                  `json:"updated"`
	DataType string                                  `json:"datatype"`
	Format   string                                  `json:"format"`
	License  string                                  `json:"license"`
	Products map[string]SimpleStreamsManifestProduct `json:"products"`
}

func (s *SimpleStreamsManifest) ToLXD() ([]api.Image, map[string][][]string) {
	downloads := map[string][][]string{}

	images := []api.Image{}
	nameLayout := "20060102"
	eolLayout := "2006-01-02"

	for _, product := range s.Products {
		// Skip unsupported architectures
		architecture, err := osarch.ArchitectureId(product.Architecture)
		if err != nil {
			continue
		}

		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			continue
		}

		for name, version := range product.Versions {
			// Short of anything better, use the name as date (see format above)
			if len(name) < 8 {
				continue
			}

			creationDate, err := time.Parse(nameLayout, name[0:8])
			if err != nil {
				continue
			}

			var meta SimpleStreamsManifestProductVersionItem
			var rootTar SimpleStreamsManifestProductVersionItem
			var rootSquash SimpleStreamsManifestProductVersionItem
			deltas := []SimpleStreamsManifestProductVersionItem{}

			for _, item := range version.Items {
				// Identify deltas
				if item.FileType == "squashfs.vcdiff" {
					deltas = append(deltas, item)
				}

				// Skip the files we don't care about
				if !shared.StringInSlice(item.FileType, []string{"root.tar.xz", "lxd.tar.xz", "squashfs"}) {
					continue
				}

				if item.FileType == "lxd.tar.xz" {
					meta = item
				} else if item.FileType == "squashfs" {
					rootSquash = item
				} else if item.FileType == "root.tar.xz" {
					rootTar = item
				}
			}

			if meta.FileType == "" || (rootTar.FileType == "" && rootSquash.FileType == "") {
				// Invalid image
				continue
			}

			var rootfsSize int64
			metaPath := meta.Path
			metaHash := meta.HashSha256
			metaSize := meta.Size
			rootfsPath := ""
			rootfsHash := ""
			fields := strings.Split(meta.Path, "/")
			filename := fields[len(fields)-1]
			size := meta.Size
			fingerprint := ""

			if rootSquash.FileType != "" {
				if meta.LXDHashSha256SquashFs != "" {
					fingerprint = meta.LXDHashSha256SquashFs
				} else {
					fingerprint = meta.LXDHashSha256
				}
				size += rootSquash.Size
				rootfsPath = rootSquash.Path
				rootfsHash = rootSquash.HashSha256
				rootfsSize = rootSquash.Size
			} else {
				if meta.LXDHashSha256RootXz != "" {
					fingerprint = meta.LXDHashSha256RootXz
				} else {
					fingerprint = meta.LXDHashSha256
				}
				size += rootTar.Size
				rootfsPath = rootTar.Path
				rootfsHash = rootTar.HashSha256
				rootfsSize = rootTar.Size
			}

			if size == 0 || filename == "" || fingerprint == "" {
				// Invalid image
				continue
			}

			// Generate the actual image entry
			description := fmt.Sprintf("%s %s %s", product.OperatingSystem, product.ReleaseTitle, product.Architecture)
			if version.Label != "" {
				description = fmt.Sprintf("%s (%s)", description, version.Label)
			}
			description = fmt.Sprintf("%s (%s)", description, name)

			image := api.Image{}
			image.Architecture = architectureName
			image.Public = true
			image.Size = size
			image.CreatedAt = creationDate
			image.UploadedAt = creationDate
			image.Filename = filename
			image.Fingerprint = fingerprint
			image.Properties = map[string]string{
				"os":           product.OperatingSystem,
				"release":      product.Release,
				"version":      product.Version,
				"architecture": product.Architecture,
				"label":        version.Label,
				"serial":       name,
				"description":  description,
			}

			// Add the provided aliases
			if product.Aliases != "" {
				image.Aliases = []api.ImageAlias{}
				for _, entry := range strings.Split(product.Aliases, ",") {
					image.Aliases = append(image.Aliases, api.ImageAlias{Name: entry})
				}
			}

			// Clear unset properties
			for k, v := range image.Properties {
				if v == "" {
					delete(image.Properties, k)
				}
			}

			// Attempt to parse the EOL
			image.ExpiresAt = time.Unix(0, 0).UTC()
			if product.SupportedEOL != "" {
				eolDate, err := time.Parse(eolLayout, product.SupportedEOL)
				if err == nil {
					image.ExpiresAt = eolDate
				}
			}

			imgDownloads := [][]string{
				{metaPath, metaHash, "meta", fmt.Sprintf("%d", metaSize)},
				{rootfsPath, rootfsHash, "root", fmt.Sprintf("%d", rootfsSize)}}

			// Add the deltas
			for _, delta := range deltas {
				srcImage, ok := product.Versions[delta.DeltaBase]
				if !ok {
					continue
				}

				var srcFingerprint string
				for _, item := range srcImage.Items {
					if item.FileType != "lxd.tar.xz" {
						continue
					}

					srcFingerprint = item.LXDHashSha256SquashFs
					break
				}

				if srcFingerprint == "" {
					continue
				}

				imgDownloads = append(imgDownloads, []string{
					delta.Path,
					delta.HashSha256,
					fmt.Sprintf("root.delta-%s", srcFingerprint),
					fmt.Sprintf("%d", delta.Size)})
			}

			downloads[fingerprint] = imgDownloads
			images = append(images, image)
		}
	}

	return images, downloads
}

type SimpleStreamsManifestProduct struct {
	Aliases         string                                         `json:"aliases"`
	Architecture    string                                         `json:"arch"`
	OperatingSystem string                                         `json:"os"`
	Release         string                                         `json:"release"`
	ReleaseCodename string                                         `json:"release_codename"`
	ReleaseTitle    string                                         `json:"release_title"`
	Supported       bool                                           `json:"supported"`
	SupportedEOL    string                                         `json:"support_eol"`
	Version         string                                         `json:"version"`
	Versions        map[string]SimpleStreamsManifestProductVersion `json:"versions"`
}

type SimpleStreamsManifestProductVersion struct {
	PublicName string                                             `json:"pubname"`
	Label      string                                             `json:"label"`
	Items      map[string]SimpleStreamsManifestProductVersionItem `json:"items"`
}

type SimpleStreamsManifestProductVersionItem struct {
	Path                  string `json:"path"`
	FileType              string `json:"ftype"`
	HashMd5               string `json:"md5"`
	HashSha256            string `json:"sha256"`
	LXDHashSha256         string `json:"combined_sha256"`
	LXDHashSha256RootXz   string `json:"combined_rootxz_sha256"`
	LXDHashSha256SquashFs string `json:"combined_squashfs_sha256"`
	Size                  int64  `json:"size"`
	DeltaBase             string `json:"delta_base"`
}

type SimpleStreamsIndex struct {
	Format  string                              `json:"format"`
	Index   map[string]SimpleStreamsIndexStream `json:"index"`
	Updated string                              `json:"updated"`
}

type SimpleStreamsIndexStream struct {
	Updated  string   `json:"updated"`
	DataType string   `json:"datatype"`
	Path     string   `json:"path"`
	Products []string `json:"products"`
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
		cachedManifest: map[string]*SimpleStreamsManifest{},
		useragent:      useragent,
	}
}

type SimpleStreams struct {
	http      *http.Client
	url       string
	useragent string

	cachedIndex    *SimpleStreamsIndex
	cachedManifest map[string]*SimpleStreamsManifest
	cachedImages   []api.Image
	cachedAliases  map[string]*api.ImageAliasesEntry
}

func (s *SimpleStreams) parseIndex() (*SimpleStreamsIndex, error) {
	if s.cachedIndex != nil {
		return s.cachedIndex, nil
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
	ssIndex := SimpleStreamsIndex{}
	err = json.Unmarshal(body, &ssIndex)
	if err != nil {
		return nil, err
	}

	s.cachedIndex = &ssIndex

	return &ssIndex, nil
}

func (s *SimpleStreams) parseManifest(path string) (*SimpleStreamsManifest, error) {
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
	ssManifest := SimpleStreamsManifest{}
	err = json.Unmarshal(body, &ssManifest)
	if err != nil {
		return nil, err
	}

	s.cachedManifest[path] = &ssManifest

	return &ssManifest, nil
}

func (s *SimpleStreams) applyAliases(images []api.Image) ([]api.Image, map[string]*api.ImageAliasesEntry, error) {
	aliases := map[string]*api.ImageAliasesEntry{}

	sort.Sort(ssSortImage(images))

	defaultOS := ""
	for k, v := range ssDefaultOS {
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
