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

	leaderInfo, err := s.LeaderInfo()
	if err != nil {
		// Log warning but don't return here so that any local token operations are pruned.
		logger.Warn("Failed to get database leader details", logger.Ctx{"err": err})
	}

	var expiredPendingTLSIdentities []cluster.Identity
	if leaderInfo != nil && leaderInfo.Leader {
		expiredPendingTLSIdentities, err = getExpiredPendingIdentities(ctx, s)
		if err != nil {
			// Log warning but don't return here so that any local token operations are pruned.
			logger.Warn("Failed to retrieve expired pending TLS identities during removal of expired tokens task", logger.Ctx{"err": err})
		}
	}

	if len(expiredTokenOps) == 0 && len(expiredPendingTLSIdentities) == 0 {
		// Nothing to do
		return
	}

	opRun := func(op *operations.Operation) error {
		for _, op := range expiredTokenOps {
			_, err := op.Cancel()
			if err != nil {
				logger.Warn("Failed removing expired token", logger.Ctx{"err": err, "operation": op.ID()})
			}
		}

		err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			for _, expiredPendingTLSIdentity := range expiredPendingTLSIdentities {
				err := cluster.DeleteIdentity(ctx, tx.Tx(), api.AuthenticationMethodTLS, expiredPendingTLSIdentity.Identifier)
				if err != nil {
					logger.Warn("Failed removing pending TLS identity", logger.Ctx{"err": err, "operation": op.ID(), "identity": expiredPendingTLSIdentity.Identifier})
				}
			}

			return nil
		})
		if err != nil {
			logger.Warn("Failed removing pending TLS identities", logger.Ctx{"err": err, "operation": op.ID()})
		}

		return nil
	}

	op, err := operations.OperationCreate(context.Background(), s, "", operations.OperationClassTask, operationtype.RemoveExpiredTokens, nil, nil, opRun, nil, nil)
	if err != nil {
		logger.Warn("Failed creating remove expired tokens operation", logger.Ctx{"err": err})
		return
	}

	logger.Info("Removing expired tokens")

	err = op.Start()
	if err != nil {
		logger.Warn("Failed starting remove expired tokens operation", logger.Ctx{"err": err})
		return
	}

	err = op.Wait(ctx)
	if err != nil {
		logger.Warn("Failed removing expired tokens", logger.Ctx{"err": err})
		return
	}

	logger.Debug("Done removing expired tokens")
}

func getExpiredPendingIdentities(ctx context.Context, s *state.State) ([]cluster.Identity, error) {
	var pendingTLSIdentities []cluster.Identity
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		dbPendingClientIdentityType := cluster.IdentityType(api.IdentityTypeCertificateClientPending)
		dbPendingClusterLinkIdentityType := cluster.IdentityType(api.IdentityTypeCertificateClusterLinkPending)
		pendingTLSIdentities, err = cluster.GetIdentitys(ctx, tx.Tx(), cluster.IdentityFilter{Type: &dbPendingClientIdentityType}, cluster.IdentityFilter{Type: &dbPendingClusterLinkIdentityType})
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	expiredPendingTLSIdentities := make([]cluster.Identity, 0, len(pendingTLSIdentities))
	for _, pendingTLSIdentity := range pendingTLSIdentities {
		metadata, err := pendingTLSIdentity.PendingTLSMetadata()
		if err == nil && (metadata.Expiry.IsZero() || metadata.Expiry.After(time.Now())) {
			continue // Token has not expired.
		}

		if err != nil {
			// In this case, regardless of the error returned by PendingTLSMetadata, we want to remove the pending identity.
			// This is because a) we know that it is a pending identity because our query filtered for only that identity type,
			// and b) if unmarshalling the metadata failed then the pending identity is invalid (it cannot be activated because
			// we cannot check it's expiry). Therefore we log the error and continue to append it to our list.
			logger.Warn("Failed to unmarshal pending TLS identity metadata", logger.Ctx{"err": err})
		}

		// If it's expired it should be removed.
		expiredPendingTLSIdentities = append(expiredPendingTLSIdentities, pendingTLSIdentity)
	}

	return expiredPendingTLSIdentities, nil
}

func autoRemoveExpiredTokensTask(stateFunc func() *state.State) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := stateFunc()

		autoRemoveExpiredTokens(ctx, s)
	}

	return f, task.Every(time.Hour)
}
