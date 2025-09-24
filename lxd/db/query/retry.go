package query

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

const maxRetries = 250

// Retry wraps a function that interacts with the database, and retries it in
// case a transient error is hit.
//
// This should by typically used to wrap transactions.
func Retry(ctx context.Context, f func(ctx context.Context) error) error {
	var err error
	for i := range maxRetries {
		err = f(ctx)
		if err == nil {
			// The function succeeded, we're done here.
			break
		}

		if errors.Is(err, context.Canceled) {
			// The function was canceled, don't retry.
			break
		}

		// No point in re-trying or logging a no-row or not found error.
		if errors.Is(err, sql.ErrNoRows) || api.StatusErrorCheck(err, http.StatusNotFound) {
			break
		}

		// Process actual errors.
		if !IsRetriableError(err) {
			logger.Debug("Database error", logger.Ctx{"err": err})
			break
		}

		if i == maxRetries {
			logger.Warn("Database error, giving up", logger.Ctx{"attempt": i, "err": err})
			break
		}

		logger.Debug("Database error, retrying", logger.Ctx{"attempt": i, "err": err})
		time.Sleep(jitterDeviation(0.8, 100*time.Millisecond))
	}

	return err
}

func jitterDeviation(factor float64, duration time.Duration) time.Duration {
	floor := int64(math.Floor(float64(duration) * (1 - factor)))
	ceil := int64(math.Ceil(float64(duration) * (1 + factor)))
	return time.Duration(rand.Int64N(ceil-floor) + floor)
}

// IsRetriableError returns true if the given error might be transient and the
// interaction can be safely retried.
func IsRetriableError(err error) bool {
	// Unwrap errors one at a time.
	for ; err != nil; err = errors.Unwrap(err) {
		if strings.Contains(err.Error(), "database is locked") {
			return true
		}

		if strings.Contains(err.Error(), "bad connection") {
			return true
		}

		if strings.Contains(err.Error(), "checkpoint in progress") {
			return true
		}
	}

	return false
}
