package request

import (
	"context"
	"fmt"
	"net/http"
)

// GetCtxValue gets a value of type T from the context using the given key.
func GetCtxValue[T any](ctx context.Context, key CtxKey) (T, error) {
	var empty T
	valueAny := ctx.Value(key)
	if valueAny == nil {
		return empty, fmt.Errorf("Failed to get expected value %q from context", key)
	}

	value, ok := valueAny.(T)
	if !ok {
		return empty, fmt.Errorf("Value for context key %q has incorrect type (expected %T, got %T)", key, empty, valueAny)
	}

	return value, nil
}

// SetCtxValue sets the given value in the request context with the given key.
func SetCtxValue(r *http.Request, key CtxKey, value any) {
	rWithCtx := r.WithContext(context.WithValue(r.Context(), key, value))
	*r = *rWithCtx
}
