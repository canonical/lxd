# API extensions

The changes below were introduced to the LXD API after the 1.0 API was finalized.

They are all backward compatible and can be detected by client tools by
looking at the api\_extensions field in GET /1.0/.


## id\_map
Enables setting the `security.idmap.isolated` and `security.idmap.isolated`,
`security.idmap.size`, and `raw.id_map` fields.

## id\_map\_base
This introduces a new security.idmap.base allowing the user to skip the
map auto-selection process for isolated containers and specify what host
uid/gid to use as the base.
