package main

import (
	"context"
	"net/http"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/task"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

func removeTokenHandler(d *Daemon, r *http.Request) response.Response {
	autoRemoveExpiredTokens(r.Context(), d.State())
	return response.EmptySyncResponse
}

func autoRemoveExpiredTokens(ctx context.Context, s *state.State) {
	expiredTokenOps := make([]*operations.Operation, 0)

	for _, op := range operations.Clone() {
		// Only consider token operations
		if op.Type() != operationtype.ClusterJoinToken && op.Type() != operationtype.CertificateAddToken {
			continue
		}

		// Instead of cancelling the operation here, we add it to a list of expired token operations.
		// This allows us to only show log messages if there are expired tokens.
		expiry, ok := op.Metadata()["expiresAt"].(time.Time)
		if ok && time.Now().After(expiry) {
			expiredTokenOps = append(expiredTokenOps, op)
		}
	}

	isLeader, _, err := s.LeaderInfo()
	if err != nil {
		logger.Error("Failed to get database leader details", logger.Ctx{"err": err})
	}

	var expiredPendingTLSIdentities []cluster.Identity
	if isLeader {
		var pendingTLSIdentities []cluster.Identity
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			dbPendingIdentityType := cluster.IdentityType(api.IdentityTypeCertificateClientPending)
			pendingTLSIdentities, err = cluster.GetIdentitys(ctx, tx.Tx(), cluster.IdentityFilter{Type: &dbPendingIdentityType})
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			logger.Error("Failed to retrieve pending TLS identities during removal of expired tokens task", logger.Ctx{"err": err})
		}

		expiredPendingTLSIdentities = make([]cluster.Identity, 0, len(pendingTLSIdentities))
		for _, pendingTLSIdentity := range pendingTLSIdentities {
			metadata, err := pendingTLSIdentity.PendingTLSMetadata()
			if err != nil {
				logger.Error("Failed to unmarshal pending TLS identity metadata", logger.Ctx{"err": err})
				expiredPendingTLSIdentities = append(expiredPendingTLSIdentities, pendingTLSIdentity)
				continue
			}

			if !metadata.Expiry.IsZero() && metadata.Expiry.Before(time.Now()) {
				expiredPendingTLSIdentities = append(expiredPendingTLSIdentities, pendingTLSIdentity)
			}
		}
	}

	if len(expiredTokenOps) == 0 && len(expiredPendingTLSIdentities) == 0 {
		return
	}

	opRun := func(op *operations.Operation) error {
		for _, op := range expiredTokenOps {
			_, err := op.Cancel()
			if err != nil {
				logger.Debug("Failed removing expired token", logger.Ctx{"err": err, "id": op.ID()})
			}
		}

		err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			for _, expiredPendingTLSIdentity := range expiredPendingTLSIdentities {
				err := cluster.DeleteIdentity(ctx, tx.Tx(), api.AuthenticationMethodTLS, expiredPendingTLSIdentity.Identifier)
				if err != nil {
					logger.Debug("Failed removing pending TLS identity", logger.Ctx{"err": err, "id": op.ID()})
				}
			}

			return nil
		})
		if err != nil {
			logger.Debug("Failed removing pending TLS identities", logger.Ctx{"err": err, "id": op.ID()})
		}

		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.RemoveExpiredTokens, nil, nil, opRun, nil, nil, nil)
	if err != nil {
		logger.Error("Failed creating remove expired tokens operation", logger.Ctx{"err": err})
		return
	}

	logger.Info("Removing expired tokens")

	err = op.Start()
	if err != nil {
		logger.Error("Failed starting remove expired tokens operation", logger.Ctx{"err": err})
		return
	}

	err = op.Wait(ctx)
	if err != nil {
		logger.Error("Failed removing expired tokens", logger.Ctx{"err": err})
		return
	}

	logger.Debug("Done removing expired tokens")
}

func autoRemoveExpiredTokensTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		autoRemoveExpiredTokens(ctx, d.State())
	}

	return f, task.Every(time.Hour)
}
