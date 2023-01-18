(images-profiles)=
# How to associate profiles with an image

## Profiles

A list of profiles can be associated with an image using the `lxc image edit`
command. After associating profiles with an image, an instance launched
using the image will have the profiles applied in order. If `nil` is passed
as the list of profiles, only the `default` profile will be associated with
the image. If an empty list is passed, then no profile will be associated
with the image, not even the `default` profile. An image's associated
profiles can be overridden when launching an instance by using the
`--profile` and the `--no-profiles` flags to `lxc launch`.
