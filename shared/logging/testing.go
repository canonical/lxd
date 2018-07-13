package logging

import (
	"fmt"
	"testing"

	log "github.com/lxc/lxd/shared/log15"
)

// Testing installs a global logger that emits messages using the t.Logf method
// of the given testing.T instance.
//
// Return a function that can be used to restore whatever global logger was
// previously in place.
func Testing(t *testing.T) func() {
	logger := log.New()
	logger.SetHandler(&testingHandler{t: t})
	return SetLogger(logger)
}

type testingHandler struct {
	t *testing.T
}

func (h *testingHandler) Log(r *log.Record) error {
	// Render all key-value pairs in the context
	ctx := ""
	for i, v := range r.Ctx {
		if i%2 == 0 {
			ctx += fmt.Sprintf(" %v", v)
		} else {
			ctx += fmt.Sprintf("=%v", v)
		}
	}

	h.t.Logf("%s %s %s%s", r.Time.Format("15:04:05.000"), r.Lvl, r.Msg, ctx)

	return nil
}
