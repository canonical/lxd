package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/lifecycle"
	"github.com/canonical/lxd/lxd/locking"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/registry"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/rsync"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
	"github.com/canonical/lxd/shared/ioprogress"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// ImageDownloadArgs used with ImageDownload.
type ImageDownloadArgs struct {
	ProjectName       string
	Server            string // Deprecated: Use ImageRegistry.
	Protocol          string // Deprecated: Use ImageRegistry.
	Certificate       string // Deprecated: Use ImageRegistry.
	ImageRegistry     string
	Secret            string
	Alias             string
	Type              string
	SetCached         bool
	PreferCached      bool
	AutoUpdate        bool
	Public            bool
	StoragePool       string
	Budget            int64
	SourceProjectName string
	UserRequested     bool
	// Proxy overrides the daemon-level proxy function when set.
	// Use a no-op proxy function for intra-cluster connections to bypass the configured HTTP proxy.
	Proxy func(req *http.Request) (*url.URL, error)
}

// imageOperationLock acquires a lock for operating on an image and returns the unlock function.
func imageOperationLock(fingerprint string) (locking.UnlockFunc, error) {
	l := logger.AddContext(logger.Ctx{"fingerprint": fingerprint})
	l.Debug("Acquiring lock for image")
	defer l.Debug("Lock acquired for image")

	return locking.Lock(context.TODO(), "ImageOperation_"+fingerprint)
}

// ImageDownload resolves the given image alias or fingerprint and if not in the database, downloads it.
//
// If args.ImageRegistry is provided, it attempts to fetch the image from the specified remote registry.
// It will resolve aliases and fetch initial metadata from the remote server.
//
// If args.ImageRegistry is empty, it assumes a local image and attempts to resolve the provided alias or
// fingerprint against the local database. It prioritizes checking the args.SourceProjectName for the alias,
// and falls back to checking args.ProjectName.
//
// Finally, if the image isn't already present locally, it will download the image to the local storage
// and save it in the database.
func ImageDownload(ctx context.Context, s *state.State, op *operations.Operation, args *ImageDownloadArgs) (*api.Image, error) {
	l := logger.AddContext(logger.Ctx{"image": args.Alias, "member": s.ServerName, "project": args.ProjectName, "pool": args.StoragePool, "image_registry": args.ImageRegistry})

	var err error
	var imageRegistry *api.ImageRegistry
	var remote lxd.ImageServer
	var info *api.Image
	var sourceAliases []api.ImageAlias

	// Copy so that local modifications aren't propagated to args.
	alias := args.Alias

	// Default the fingerprint to the alias string we received.
	fp := alias

	if args.ImageRegistry != "" {
		// Fetch the source image registry details.
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			dbImageRegistry, err := cluster.GetImageRegistry(ctx, tx.Tx(), args.ImageRegistry)
			if err != nil {
				return fmt.Errorf("Failed fetching image registry %q: %w", args.ImageRegistry, err)
			}

			config, err := cluster.GetImageRegistryConfig(ctx, tx.Tx(), &dbImageRegistry.ID)
			if err != nil {
				return fmt.Errorf("Failed loading image registry config: %w", err)
			}

			imageRegistry = dbImageRegistry.ToAPI(config)
			return nil
		})
		if err != nil {
			return nil, err
		}

		// Connect to the remote image server.
		remote, err = registry.ConnectImageRegistry(ctx, s, *imageRegistry)
		if err != nil {
			return nil, err
		}

		// For public images, resolve aliases and fetch initial metadata from the remote server.
		if args.Secret == "" {
			// Look for a matching alias on the remote server.
			entry, _, err := remote.GetImageAliasType(args.Type, fp)
			if err == nil {
				fp = entry.Target
			}

			// Expand partial fingerprints and fetch full image info.
			info, _, err = remote.GetImage(fp)
			if err != nil {
				return nil, fmt.Errorf("Failed getting remote image info: %w", err)
			}

			fp = info.Fingerprint
			sourceAliases = info.Aliases
		} else {
			// Fetch information for a private image using the provided secret.
			info, _, err = remote.GetPrivateImage(fp, args.Secret)
			if err != nil {
				return nil, fmt.Errorf("Failed getting remote image info: %w", err)
			}

			fp = info.Fingerprint
			sourceAliases = info.Aliases

			// Set alias to equal fingerprint so that we don't save this remote as an image source or try to auto-update
			// the image (we can't get it again since it is private). In this case the alias may have actually been an
			// image fingerprint prefix.
			alias = info.Fingerprint
		}

		// Ensure the resolved fingerprint is valid.
		err = validateImageFingerprint(fp)
		if err != nil {
			return nil, err
		}
	} else {
		// When no registry is provided, we attempt to resolve the provided fingerprint or alias locally.
		_ = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Check if the name matches an alias in the source project.
			if args.SourceProjectName != "" {
				_, entry, err := tx.GetImageAlias(ctx, args.SourceProjectName, fp, true)
				if err == nil {
					fp = entry.Target
					return nil
				}
			}

			// Check if the name matches an alias in the target project (if different from source).
			if args.ProjectName != "" && args.ProjectName != args.SourceProjectName {
				_, entry, err := tx.GetImageAlias(ctx, args.ProjectName, fp, true)
				if err == nil {
					fp = entry.Target
					return nil
				}
			}

			return nil
		})
	}

	// Ensure we are the only ones operating on this image.
	unlock, err := imageOperationLock(fp)
	if err != nil {
		return nil, err
	}

	defer unlock()

	// If auto-update is on and we're being given the image by
	// alias, try to use a locally cached image matching the given
	// server/protocol/alias, regardless of whether it's stale or
	// not (we can assume that it will be not *too* stale since
	// auto-update is on).
	interval := s.GlobalConfig.ImagesAutoUpdateIntervalHours()

	if args.PreferCached && interval > 0 && alias != fp {
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			for _, architecture := range s.OS.Architectures {
				cachedFingerprint, err := tx.GetCachedImageSourceFingerprint(ctx, args.ImageRegistry, alias, args.Type, architecture)
				if err == nil && cachedFingerprint != fp {
					fp = cachedFingerprint
					break
				}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var imgInfo *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if the image already exists in this project (partial hash match).
		_, imgInfo, err = tx.GetImage(ctx, fp, cluster.ImageFilter{Project: &args.ProjectName})

		return err
	})
	if err == nil {
		var nodeAddress string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check if the image is available locally or it's on another node.
			nodeAddress, err = tx.LocateImage(ctx, imgInfo.Fingerprint)

			return err
		})
		if err != nil {
			return nil, fmt.Errorf("Failed locating image %q in the cluster: %w", imgInfo.Fingerprint, err)
		}

		if nodeAddress != "" {
			// The image is available from another node, let's try to import it.
			err = instanceImageTransfer(ctx, s, args.ProjectName, args.ProjectName, imgInfo.Fingerprint, nodeAddress)
			if err != nil {
				return nil, fmt.Errorf("Failed transferring image %q from %q: %w", imgInfo.Fingerprint, nodeAddress, err)
			}

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// As the image record already exists in the project, just add the node ID to the image.
				return tx.AddImageToLocalNode(ctx, args.ProjectName, imgInfo.Fingerprint)
			})
			if err != nil {
				return nil, fmt.Errorf("Failed adding transferred image %q to local cluster member: %w", imgInfo.Fingerprint, err)
			}
		}
	} else if response.IsNotFoundError(err) {
		// If the image doesn't exist in the target project, check across all other projects.
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, imgInfo, err = tx.GetImageFromAnyProject(ctx, fp)
			return err
		})

		if err == nil {
			// Image found in another project. Resolve its current location and source aliases
			// before preparing the transfer.
			var nodeAddress string
			otherProject := imgInfo.Project
			sourceInfo := imgInfo.UpdateSource

			sourceAliases = imgInfo.Aliases

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Check if the image is already available locally or on another node. We need to do this before
				// inserting the record for the new project to avoid finding ourselves in the search results.
				nodeAddress, err = tx.LocateImage(ctx, imgInfo.Fingerprint)
				if err != nil {
					return fmt.Errorf("Locate image %q in the cluster: %w", imgInfo.Fingerprint, err)
				}

				// Create the image record in the database for the new target project.
				err = tx.CreateImage(ctx, args.ProjectName, imgInfo.Fingerprint, imgInfo.Filename, imgInfo.Size, args.Public, imgInfo.AutoUpdate, imgInfo.Architecture, imgInfo.CreatedAt, imgInfo.ExpiresAt, imgInfo.Properties, imgInfo.Type, nil)
				if err != nil {
					return fmt.Errorf("Failed creating image record for project: %w", err)
				}

				// Mark the image as "cached" if downloading for an instance.
				if args.SetCached {
					err = tx.SetImageCachedAndLastUseDate(ctx, args.ProjectName, imgInfo.Fingerprint, time.Now().UTC())
					if err != nil {
						return fmt.Errorf("Failed setting cached flag and last use date: %w", err)
					}
				}

				var id int

				id, imgInfo, err = tx.GetImage(ctx, fp, cluster.ImageFilter{Project: &args.ProjectName})
				if err != nil {
					return err
				}

				// Restore the source aliases so the caller can handle them (e.g., if --copy-aliases is used).
				imgInfo.Aliases = sourceAliases

				imageRegistry := args.ImageRegistry
				imageAlias := alias

				// Use the existing image's update source if no registry is provided in the args.
				if imageRegistry == "" && sourceInfo != nil {
					imageRegistry = sourceInfo.ImageRegistry
					imageAlias = sourceInfo.Alias
				}

				if imageRegistry != "" {
					return tx.CreateImageSource(ctx, id, imageRegistry, imageAlias)
				}

				return nil
			})
			if err != nil {
				return nil, err
			}

			// If the image files exist on another cluster node, initiate a transfer.
			if nodeAddress != "" {
				err = instanceImageTransfer(ctx, s, args.ProjectName, otherProject, imgInfo.Fingerprint, nodeAddress)
				if err != nil {
					return nil, fmt.Errorf("Failed transferring image: %w", err)
				}
			} else {
				// If the image files are available locally but in another project with a different storage volume,
				// perform a local file copy between storage paths.
				if s.LocalConfig.StorageImagesVolume(otherProject) != s.LocalConfig.StorageImagesVolume(args.ProjectName) {
					sourcePath := filepath.Join(s.ImagesStoragePath(otherProject), imgInfo.Fingerprint)
					destPath := s.ImagesStoragePath(args.ProjectName)

					_, err = rsync.CopyFile(sourcePath, destPath, "", false)
					if err != nil {
						return nil, fmt.Errorf("Failed copying image files from other project: %w", err)
					}

					if shared.PathExists(sourcePath + ".rootfs") {
						_, err = rsync.CopyFile(sourcePath+".rootfs", destPath, "", false)
						if err != nil {
							return nil, fmt.Errorf("Failed copying image files from other project: %w", err)
						}
					}
				}
			}
		}
	}

	if imgInfo != nil {
		info = imgInfo
		l = l.AddContext(logger.Ctx{"fingerprint": info.Fingerprint, "autoUpdate": info.AutoUpdate, "imgProject": info.Project})
		l.Debug("Image already exists in the DB")

		// Pass the source aliases back to the caller so they can be processed (e.g., if --copy-aliases is used).
		info.Aliases = sourceAliases

		var poolID int64
		var poolIDs []int64
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// If the image already exists, is cached and that it is
			// requested to be downloaded from an explicit `image copy` operation, then disable its `cache` parameter
			// so that it won't be candidate for auto removal.
			if imgInfo.Cached && args.UserRequested {
				err = tx.UnsetImageCached(ctx, args.ProjectName, imgInfo.Fingerprint)
				if err != nil {
					return err
				}
			}

			if args.StoragePool != "" {
				// Get the ID of the target storage pool.
				poolID, err = tx.GetStoragePoolID(ctx, args.StoragePool)
				if err != nil {
					return err
				}

				// Check if the image is already in the pool.
				poolIDs, err = tx.GetPoolsWithImage(ctx, info.Fingerprint)

				return err
			}

			return nil
		})
		if err != nil {
			return nil, err
		}

		// If not requested in a particular pool, we're done.
		if args.StoragePool == "" {
			return info, nil
		}

		if slices.Contains(poolIDs, poolID) {
			l.Debug("Image already exists on storage pool")
			return info, nil
		}

		// Import the image in the pool.
		l.Debug("Image does not exist on storage pool")

		err = imageCreateInPool(ctx, s, info, args.StoragePool, args.ProjectName)
		if err != nil {
			l.Debug("Failed creating image on storage pool", logger.Ctx{"err": err})
			return nil, fmt.Errorf("Failed creating image %q on storage pool %q: %w", info.Fingerprint, args.StoragePool, err)
		}

		l.Debug("Created image on storage pool")
		return info, nil
	}

	if args.ImageRegistry == "" {
		return nil, fmt.Errorf("Image %q not found in the database", fp)
	}

	// Begin downloading
	if op != nil {
		l = l.AddContext(logger.Ctx{"trigger": op.URL(), "operation": op.ID()})
	}

	l.Info("Downloading image")

	// Cleanup any leftover from a past attempt
	destRoot, err := os.OpenRoot(s.ImagesStoragePath(args.ProjectName))
	if err != nil {
		return nil, err
	}

	defer func() { _ = destRoot.Close() }()

	destFile, err := destRoot.Create(fp)
	if err != nil {
		return nil, err
	}

	defer func() { _ = destFile.Close() }()

	destRootfsFileName := fp + ".rootfs"
	destRootfsFile, err := destRoot.Create(destRootfsFileName)
	if err != nil {
		return nil, err
	}

	defer func() { _ = destRootfsFile.Close() }()

	reverter := revert.New()
	defer reverter.Fail()

	reverter.Add(func() {
		_ = destRoot.Remove(fp)
		_ = destRoot.Remove(destRootfsFileName)
	})

	// Setup a progress handler
	var progress ioprogress.ProgressHandler
	if op != nil {
		progress = op.ProgressHandler("download")
	}

	var canceler *cancel.HTTPRequestCanceller
	if op != nil {
		canceler = cancel.NewHTTPRequestCancellerWithContext(ctx)
	}

	// Compatibility with older LXD servers
	if info.Type == "" {
		info.Type = "container"
	}

	if args.Budget > 0 && info.Size > args.Budget {
		return nil, fmt.Errorf("Remote image with size %d exceeds allowed bugdget of %d", info.Size, args.Budget)
	}

	// Download the image
	var resp *lxd.ImageFileResponse
	imageFileRequest := lxd.ImageFileRequest{
		MetaFile:        io.WriteSeeker(destFile),
		RootfsFile:      io.WriteSeeker(destRootfsFile),
		ProgressHandler: progress,
		Canceler:        canceler,
		DeltaSourceRetriever: func(fingerprint string, file string) string {
			filename := fingerprint + "." + file
			_, err := destRoot.Lstat(filename)
			if err == nil {
				return filepath.Join(destRoot.Name(), filename)
			}

			return ""
		},
	}

	if args.Secret != "" {
		resp, err = remote.GetPrivateImageFile(fp, args.Secret, imageFileRequest)
	} else {
		resp, err = remote.GetImageFile(fp, imageFileRequest)
	}

	if err != nil {
		return nil, err
	}

	// Truncate down to size
	if resp.RootfsSize > 0 {
		err = destRootfsFile.Truncate(resp.RootfsSize)
		if err != nil {
			return nil, err
		}
	}

	err = destFile.Truncate(resp.MetaSize)
	if err != nil {
		return nil, err
	}

	// Deal with unified images
	if resp.RootfsSize == 0 {
		err := destRoot.Remove(destRootfsFileName)
		if err != nil {
			return nil, err
		}
	}

	err = destFile.Close()
	if err != nil {
		return nil, err
	}

	err = destRootfsFile.Close()
	if err != nil {
		return nil, err
	}

	// Override visiblity
	info.Public = args.Public

	// We want to enable auto-update only if we were passed an
	// alias name, so we can figure when the associated
	// fingerprint changes in the remote.
	if alias != fp {
		info.AutoUpdate = args.AutoUpdate
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry
		return tx.CreateImage(ctx, args.ProjectName, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type, nil)
	})
	if err != nil {
		return nil, fmt.Errorf("Failed creating image record: %w", err)
	}

	// Image is in the DB now, don't wipe on-disk files on failure
	reverter.Success()

	// Record the image source
	if alias != fp {
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			id, _, err := tx.GetImage(ctx, fp, cluster.ImageFilter{Project: &args.ProjectName})
			if err != nil {
				return err
			}

			return tx.CreateImageSource(ctx, id, args.ImageRegistry, alias)
		})
		if err != nil {
			return nil, err
		}
	}

	// Import into the requested storage pool
	if args.StoragePool != "" {
		err = imageCreateInPool(ctx, s, info, args.StoragePool, args.ProjectName)
		if err != nil {
			return nil, err
		}
	}

	// Mark the image as "cached" if downloading for an instance
	if args.SetCached {
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.SetImageCachedAndLastUseDate(ctx, args.ProjectName, fp, time.Now().UTC())
		})
		if err != nil {
			return nil, fmt.Errorf("Failed setting cached flag and last use date: %w", err)
		}
	}

	l.Info("Image downloaded")

	var lifecycleRequestor *api.EventLifecycleRequestor
	if op != nil {
		lifecycleRequestor = op.EventLifecycleRequestor()
	} else {
		lifecycleRequestor = request.CreateRequestor(ctx)
	}

	s.Events.SendLifecycle(args.ProjectName, lifecycle.ImageCreated.Event(info.Fingerprint, args.ProjectName, lifecycleRequestor, logger.Ctx{"type": info.Type}))

	info.Aliases = sourceAliases

	return info, nil
}
