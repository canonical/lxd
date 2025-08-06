package cluster

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthSecrets(t *testing.T) {
	// Test setup
	db := newDB(t)
	doTx := func(f func(ctx context.Context, tx *sql.Tx)) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		tx, err := db.Begin()
		require.NoError(t, err)

		f(ctx, tx)
		require.NoError(t, tx.Commit())
	}

	// Create two secrets, secret1 is the first to be created (two months ago), secret two is the second to be created
	// (one month ago).
	now := time.Now().UTC()
	secret1 := newAuthSecret()
	secret1.CreationDate = now.AddDate(0, -2, 0)
	secret2 := newAuthSecret()
	secret2.CreationDate = now.AddDate(0, -1, 0)

	doTx(func(ctx context.Context, tx *sql.Tx) {
		id, err := createCoreAuthSecret(ctx, tx, secret1)
		require.NoError(t, err)
		secret1.ID = id

		id, err = createCoreAuthSecret(ctx, tx, secret2)
		require.NoError(t, err)
		secret2.ID = id
	})

	// Create an AuthSecrets slice with the most recent secret first.
	authSecrets := AuthSecrets{secret2, secret1}

	// Check expiry validation
	err := authSecrets.Validate("20d")
	require.Error(t, err)
	require.Equal(t, "Secret has expired", err.Error())

	// Rotate the secrets
	var rotatedSecrets AuthSecrets
	doTx(func(ctx context.Context, tx *sql.Tx) {
		var err error
		rotatedSecrets, err = authSecrets.Rotate(ctx, tx)
		require.NoError(t, err)
	})

	// Expect the rotated secrets to have length two, and for the most recent secret to be second in the list.
	require.Len(t, rotatedSecrets, 2)
	require.Equal(t, rotatedSecrets[1], authSecrets[0])

	// Get the current secrets from the database.
	var dbSecrets AuthSecrets
	doTx(func(ctx context.Context, tx *sql.Tx) {
		var err error
		dbSecrets, err = GetCoreAuthSecrets(ctx, tx)
		require.NoError(t, err)
	})

	// These should exactly match, in order, the rotated secrets.
	require.Len(t, dbSecrets, 2)
	for i := range dbSecrets {
		require.Equal(t, rotatedSecrets[i].ID, dbSecrets[i].ID)
		require.Equal(t, rotatedSecrets[i].Value.String(), dbSecrets[i].Value.String())
		require.Equal(t, rotatedSecrets[i].CreationDate.String(), dbSecrets[i].CreationDate.String())
	}
}
