package migration

import (
	"fmt"
	"io"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

// Type represents the migration transport type. It indicates the method by which the migration can
// take place and what optional features are available.
type Type struct {
	FSType   MigrationFSType
	Features []string
}

// VolumeSourceArgs represents the arguments needed to setup a volume migration source.
type VolumeSourceArgs struct {
	Name          string
	Snapshots     []string
	MigrationType Type
}

// VolumeTargetArgs represents the arguments needed to setup a volume migration sink.
type VolumeTargetArgs struct {
	Name          string
	Description   string
	Config        map[string]string
	Snapshots     []string
	MigrationType Type
}

// TypesToHeader converts one or more Types to a MigrationHeader. It uses the first type argument
// supplied to indicate the preferred migration method and sets the MigrationHeader's Fs type
// to that. If the preferred type is ZFS then it will also set the header's optional ZfsFeatures.
// If the fallback Rsync type is present in any of the types even if it is not preferred, then its
// optional features are added to the header's RsyncFeatures, allowing for fallback negotiation to
// take place on the farside.
func TypesToHeader(types ...Type) MigrationHeader {
	missingFeature := false
	hasFeature := true
	preferredType := types[0]
	header := MigrationHeader{Fs: &preferredType.FSType}

	// Add ZFS features if preferred type is ZFS.
	if preferredType.FSType == MigrationFSType_ZFS {
		features := ZfsFeatures{
			Compress: &missingFeature,
		}
		for _, feature := range preferredType.Features {
			if feature == "compress" {
				features.Compress = &hasFeature
			}
		}

		header.ZfsFeatures = &features
	}

	// Check all the types for an Rsync method, if found then add its features to the header's
	// RsyncFeatures list.
	for _, t := range types {
		if t.FSType != MigrationFSType_RSYNC {
			continue
		}

		features := RsyncFeatures{
			Xattrs:        &missingFeature,
			Delete:        &missingFeature,
			Compress:      &missingFeature,
			Bidirectional: &missingFeature,
		}

		for _, feature := range t.Features {
			if feature == "xattrs" {
				features.Xattrs = &hasFeature
			} else if feature == "delete" {
				features.Delete = &hasFeature
			} else if feature == "compress" {
				features.Compress = &hasFeature
			} else if feature == "bidirectional" {
				features.Bidirectional = &hasFeature
			}
		}

		header.RsyncFeatures = &features
	}

	return header
}

// MatchTypes attempts to find a matching migration transport type between an offered type sent
// from a remote source and the types supported by a local storage pool. If a match is found then
// a Type is returned containing the method and the matching optional features present in both.
func MatchTypes(offer MigrationHeader, ourTypes []Type) (Type, error) {
	// Find first matching type.
	for _, ourType := range ourTypes {
		if *offer.Fs != ourType.FSType {
			continue // Not a match, try the next one.
		}

		// We got a match, now extract the relevant offered features.
		var offeredFeatures []string
		if *offer.Fs == MigrationFSType_ZFS {
			offeredFeatures = offer.GetZfsFeaturesSlice()
		} else if *offer.Fs == MigrationFSType_RSYNC {
			offeredFeatures = offer.GetRsyncFeaturesSlice()
		}

		// Find common features in both our type and offered type.
		commonFeatures := []string{}
		for _, ourFeature := range ourType.Features {
			if shared.StringInSlice(ourFeature, offeredFeatures) {
				commonFeatures = append(commonFeatures, ourFeature)
			}
		}

		// Return type with combined features.
		return Type{
			FSType:   ourType.FSType,
			Features: commonFeatures,
		}, nil
	}

	// No matching transport type found.
	return Type{}, fmt.Errorf("No matching migration type found")
}

func progressWrapperRender(op *operations.Operation, key string, description string, progressInt int64, speedInt int64) {
	meta := op.Metadata()
	if meta == nil {
		meta = make(map[string]interface{})
	}

	progress := fmt.Sprintf("%s (%s/s)", units.GetByteSizeString(progressInt, 2), units.GetByteSizeString(speedInt, 2))
	if description != "" {
		progress = fmt.Sprintf("%s: %s (%s/s)", description, units.GetByteSizeString(progressInt, 2), units.GetByteSizeString(speedInt, 2))
	}

	if meta[key] != progress {
		meta[key] = progress
		op.UpdateMetadata(meta)
	}
}

// ProgressReader reports the read progress.
func ProgressReader(op *operations.Operation, key string, description string) func(io.ReadCloser) io.ReadCloser {
	return func(reader io.ReadCloser) io.ReadCloser {
		if op == nil {
			return reader
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		readPipe := &ioprogress.ProgressReader{
			ReadCloser: reader,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return readPipe
	}
}

// ProgressWriter reports the write progress.
func ProgressWriter(op *operations.Operation, key string, description string) func(io.WriteCloser) io.WriteCloser {
	return func(writer io.WriteCloser) io.WriteCloser {
		if op == nil {
			return writer
		}

		progress := func(progressInt int64, speedInt int64) {
			progressWrapperRender(op, key, description, progressInt, speedInt)
		}

		writePipe := &ioprogress.ProgressWriter{
			WriteCloser: writer,
			Tracker: &ioprogress.ProgressTracker{
				Handler: progress,
			},
		}

		return writePipe
	}
}
