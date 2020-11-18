// +build linux,cgo,!agent

package operations

import (
	"github.com/pkg/errors"

	"github.com/grant-he/lxd/lxd/db"
)

func registerDBOperation(op *Operation, opType db.OperationType) error {
	if op.state == nil {
		return nil
	}

	err := op.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.CreateOperation(op.project, op.id, opType)
		return err
	})
	if err != nil {
		return errors.Wrapf(err, "failed to add %q Operation %s to database", opType.Description(), op.id)
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state == nil {
		return nil
	}

	err := op.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.RemoveOperation(op.id)
	})

	return err
}

func getServerName(op *Operation) (string, error) {
	if op.state == nil {
		return "", nil
	}

	var serverName string
	var err error
	err = op.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		serverName, err = tx.GetLocalNodeName()
		return err
	})
	if err != nil {
		return "", err
	}

	return serverName, nil
}

func (op *Operation) sendEvent(eventMessage interface{}) {
	if op.events == nil {
		return
	}

	op.events.Send(op.project, "operation", eventMessage)
}
