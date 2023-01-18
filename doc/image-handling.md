---
discourse: 9310
---

(about-images)=
# About images

```{youtube} https://www.youtube.com/watch?v=wT7IDjo0Wgg
```

Instances are based on images, which contain a basic operating system (for example a Linux distribution) and some other LXD-related information.

## Introduction

LXD uses an image based workflow. It comes with a built-in image store
where the user or external tools can import images.

Containers are then started from those images.

It's possible to spawn remote instances using local images or local
instances using remote images. In such cases, the image may be cached
on the target LXD.

## Sources

LXD supports importing images from three different sources:

- Remote image server (LXD or simplestreams)
- Direct pushing of the image files
- File on a remote web server

## Caching

When spawning an instance from a remote image, the remote image is
downloaded into the local image store with the cached bit set. The image
will be kept locally as a private image until either it's been unused
(no new instance spawned) for the number of days set in
`images.remote_cache_expiry` or until the image's expiry is reached
whichever comes first.

LXD keeps track of image usage by updating the `last_used_at` image
property every time a new instance is spawned from the image.

## Auto-update

LXD can keep images up to date. By default, any image which comes from a
remote server and was requested through an alias will be automatically
updated by LXD. This can be changed with `images.auto_update_cached`.

On startup and then every 6 hours (unless `images.auto_update_interval`
is set), the LXD daemon will go look for more recent version of all the
images in the store which are marked as auto-update and have a recorded
source server.

When a new image is found, it is downloaded into the image store, the
aliases pointing to the old image are moved to the new one and the old
image is removed from the store.

The user can also request a particular image be kept up to date when
manually copying an image from a remote server.

If a new upstream image update is published and the local LXD has the
previous image in its cache when the user requests a new instance to be
created from it, LXD will use the previous version of the image rather
than delay the instance creation.

This behavior only happens if the current image is scheduled to be
auto-updated and can be disabled by setting `images.auto_update_interval` to 0.

## Special image properties

Image properties beginning with the prefix ***requirements*** (e.g. `requirements.XYZ`)
are used by LXD to determine the compatibility of the host system and the
instance to be created by said image. In the event that these are incompatible,
LXD will not start the instance.

At the moment, the following requirements are supported:

Key                                         | Type      | Default      | Description
:--                                         | :---      | :------      | :----------
`requirements.secureboot`                   | string    | -            | If set to `false`, indicates the image will not boot under secure boot
`requirements.cgroup`                       | string    | -            | If set to `v1`, indicates the image requires the host to run `CGroupV1`
