package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/canonical/lxd/shared/entity"
)

type triggerFunc func() (string, string)

// applyTriggers adds triggers to the database.
//
// Warning: These triggers are applied separately to the schema update mechanism. Changes to these triggers (especially their names)
// may require a patch.
func applyTriggers(ctx context.Context, tx *sql.Tx) error {
	applyTrigger := func(triggerFunc triggerFunc, entityType *entity.Type) error {
		name, stmt := triggerFunc()
		if name == "" && stmt == "" {
			return nil
		} else if name == "" || stmt == "" {
			if entityType != nil {
				return fmt.Errorf("Trigger name or SQL missing for entity type %q", *entityType)
			}

			return errors.New("Name or SQL missing from global trigger")
		}

		_, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, stmt)
		if err != nil {
			return err
		}

		return nil
	}

	for _, triggerFunc := range globalTriggers {
		err := applyTrigger(triggerFunc, nil)
		if err != nil {
			return err
		}
	}

	for entityType, entityTypeInfo := range entityTypes {
		err := applyTrigger(entityTypeInfo.onDeleteTriggerSQL, &entityType)
		if err != nil {
			return err
		}

		err = applyTrigger(entityTypeInfo.onUpdateTriggerSQL, &entityType)
		if err != nil {
			return err
		}

		err = applyTrigger(entityTypeInfo.onInsertTriggerSQL, &entityType)
		if err != nil {
			return err
		}
	}

	return nil
}

var globalTriggers = []triggerFunc{
	triggerIdentitiesCertificatesAfterDelete,
	triggerClusterLinksCertificatesAfterDelete,
}

func triggerIdentitiesCertificatesAfterDelete() (name string, stmt string) {
	name = "identities_certificates_after_delete"
	stmt = fmt.Sprintf(`
CREATE TRIGGER %s
    AFTER DELETE ON identities_certificates
	BEGIN
	DELETE FROM certificates
		WHERE certificates.id = OLD.certificate_id;
	END;
`, name)
	return name, stmt
}

func triggerClusterLinksCertificatesAfterDelete() (name string, stmt string) {
	name = "cluster_links_certificates_after_delete"
	stmt = fmt.Sprintf(`
CREATE TRIGGER %s
	AFTER DELETE ON cluster_links_certificates
	BEGIN
	DELETE FROM certificates
		WHERE certificates.id = OLD.certificate_id;
	END;
`, name)
	return name, stmt
}
