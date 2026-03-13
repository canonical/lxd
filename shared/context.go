package shared

// ctxKey is a context key type for values shared between the lxd and client packages.
type ctxKey string

// CtxMuxPathVars stores encoded path variable values set by the HTTP mux.
// It stores a map[string]string of variable name to encoded value. This allows
// path values to survive when creating new requests for internal forwarding
// (e.g., devLXD handlers calling main API handlers via client.NewRequestWithContext).
const CtxMuxPathVars ctxKey = "mux-path-vars"
