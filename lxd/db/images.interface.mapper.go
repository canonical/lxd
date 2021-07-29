//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// ImageGenerated is an interface of generated methods for Image
type ImageGenerated interface {
	// GetImages returns all available images.
	// generator: image List
	GetImages(filter ImageFilter) ([]Image, error)

	// GetImage returns the image with the given key.
	// generator: image Get
	GetImage(project string, fingerprint string) (*Image, error)
}
