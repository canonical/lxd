package simplestreams

import (
	"fmt"
	"strings"
	"time"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

// Products represents the base of download.json
type Products struct {
	ContentID string             `json:"content_id"`
	DataType  string             `json:"datatype"`
	Format    string             `json:"format"`
	License   string             `json:"license,omitempty"`
	Products  map[string]Product `json:"products"`
	Updated   string             `json:"updated,omitempty"`
}

// Product represents a single product inside download.json
type Product struct {
	Aliases         string                    `json:"aliases"`
	Architecture    string                    `json:"arch"`
	OperatingSystem string                    `json:"os"`
	Release         string                    `json:"release"`
	ReleaseCodename string                    `json:"release_codename,omitempty"`
	ReleaseTitle    string                    `json:"release_title"`
	Supported       bool                      `json:"supported,omitempty"`
	SupportedEOL    string                    `json:"support_eol,omitempty"`
	Version         string                    `json:"version,omitempty"`
	Versions        map[string]ProductVersion `json:"versions"`
}

// ProductVersion represents a particular version of a product
type ProductVersion struct {
	Items      map[string]ProductVersionItem `json:"items"`
	Label      string                        `json:"label,omitempty"`
	PublicName string                        `json:"pubname,omitempty"`
}

// ProductVersionItem represents a file/item of a particular ProductVersion
type ProductVersionItem struct {
	LXDHashSha256RootXz   string `json:"combined_rootxz_sha256,omitempty"`
	LXDHashSha256         string `json:"combined_sha256,omitempty"`
	LXDHashSha256SquashFs string `json:"combined_squashfs_sha256,omitempty"`
	FileType              string `json:"ftype"`
	HashMd5               string `json:"md5,omitempty"`
	Path                  string `json:"path"`
	HashSha256            string `json:"sha256,omitempty"`
	Size                  int64  `json:"size"`
	DeltaBase             string `json:"delta_base,omitempty"`
}

// ToLXD converts the products data into a list of LXD images and associated downloadable files
func (s *Products) ToLXD() ([]api.Image, map[string][][]string) {
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

			var meta ProductVersionItem
			var rootTar ProductVersionItem
			var rootSquash ProductVersionItem
			deltas := []ProductVersionItem{}

			for _, item := range version.Items {
				// Identify deltas
				if item.FileType == "squashfs.vcdiff" {
					deltas = append(deltas, item)
				}

				// Skip the files we don't care about
				if !shared.StringInSlice(item.FileType, []string{"root.tar.xz", "lxd.tar.xz", "lxd_combined.tar.gz", "squashfs"}) {
					continue
				}

				if item.FileType == "lxd.tar.xz" {
					meta = item
				} else if item.FileType == "squashfs" {
					rootSquash = item
				} else if item.FileType == "root.tar.xz" {
					rootTar = item
				} else if item.FileType == "lxd_combined.tar.gz" {
					meta = item
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
				if meta == rootTar {
					fingerprint = meta.HashSha256
					size = meta.Size
				} else {
					if meta.LXDHashSha256RootXz != "" {
						fingerprint = meta.LXDHashSha256RootXz
					} else {
						fingerprint = meta.LXDHashSha256
					}
					size += rootTar.Size
				}
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

			var imgDownloads [][]string
			if meta == rootTar {
				imgDownloads = [][]string{{metaPath, metaHash, "meta", fmt.Sprintf("%d", metaSize)}}
			} else {
				imgDownloads = [][]string{
					{metaPath, metaHash, "meta", fmt.Sprintf("%d", metaSize)},
					{rootfsPath, rootfsHash, "root", fmt.Sprintf("%d", rootfsSize)}}
			}

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
