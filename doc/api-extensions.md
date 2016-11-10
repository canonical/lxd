# API extensions

The changes below were introduced to the LXD API after the 1.0 API was finalized.

They are all backward compatible and can be detected by client tools by
looking at the api\_extensions field in GET /1.0/.


## id\_map
Enables setting the `security.idmap.isolated` and `security.idmap.isolated`,
`security.idmap.size`, and `raw.id_map` fields.
