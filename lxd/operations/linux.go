// +build linux,cgo,!agent

package operations

import (
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
)

func registerDBOperation(op *Operation, opType db.OperationType) error {
	if op.state == nil {
		return nil
	}

	err := op.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.OperationAdd(op.project, op.id, opType)
		return err
	})
	if err != nil {
		return errors.Wrapf(err, "failed to add Operation %s to database", op.id)
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state == nil {
		return nil
	}

	err := op.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		return tx.OperationRemove(op.id)
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
		serverName, err = tx.NodeName()
		return err
	})
	if err != nil {
		return "", err
	}

	return serverName, nil
}

func (op *Operation) sendEvent(eventMessage interface{}) {
	if op.state == nil {
		return
	}

	op.state.Events.Send(op.project, "operation", eventMessage)
}
