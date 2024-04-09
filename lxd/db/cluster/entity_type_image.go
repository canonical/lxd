package cluster

import (
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

// entityTypeImage implements entityType for an Image.
type entityTypeImage struct {
	entity.Image
}

// Code returns entityTypeCodeImage.
func (e entityTypeImage) Code() int64 {
	return entityTypeCodeImage
}

// AllURLsQuery returns a SQL query which returns entityTypeCodeImage, the ID of the Image,
// the project name of the Image, the location of the Image, and the path arguments of the
// Image in the order that they are found in its URL.
func (e entityTypeImage) AllURLsQuery() string {
	return fmt.Sprintf(`
SELECT %d, images.id, projects.name, '', json_array(images.fingerprint) 
FROM images 
JOIN projects ON images.project_id = projects.id`, e.Code())
}

// URLsByProjectQuery returns a SQL query in the same format as AllURLs, but accepts a project name bind argument as a filter.
func (e entityTypeImage) URLsByProjectQuery() string {
	return fmt.Sprintf("%s WHERE projects.name = ?", e.AllURLsQuery())
}

// URLByIDQuery returns a SQL query in the same format as AllURLs, but accepts a bind argument for the ID of the entity in the database.
func (e entityTypeImage) URLByIDQuery() string {
	return fmt.Sprintf(`%s WHERE images.id = ?`, e.AllURLsQuery())
}

// IDFromURLQuery returns a SQL query that returns the ID of the entity in the database.
// It expects the following bind arguments:
//   - An identifier for this returned row. This is because these queries are designed to work in UNION with queries of other entity types.
//   - The project name (even if the entity is not project specific, this should be passed as an empty string).
//   - The location (even if the entity is not location specific, this should be passed as an empty string).
//   - All path arguments from the URL.
func (e entityTypeImage) IDFromURLQuery() string {
	return `
SELECT ?, images.id 
FROM images 
JOIN projects ON images.project_id = projects.id 
WHERE projects.name = ? 
	AND '' = ? 
	AND images.fingerprint = ?`
}

// OnDeleteTriggerName returns the name of the trigger then runs when entities of type Image are deleted.
func (e entityTypeImage) OnDeleteTriggerName() string {
	return "on_image_delete"
}

// OnDeleteTriggerSQL  returns SQL that creates a trigger that is run when entities of type Image are deleted.
func (e entityTypeImage) OnDeleteTriggerSQL() string {
	return fmt.Sprintf(`CREATE TRIGGER %s
	AFTER DELETE ON images
	BEGIN
	DELETE FROM auth_groups_permissions 
		WHERE entity_type = %d 
		AND entity_id = OLD.id;
	DELETE FROM warnings
		WHERE entity_type_code = %d
		AND entity_id = OLD.id;
	END
`, e.OnDeleteTriggerName(), e.Code(), e.Code())
}
