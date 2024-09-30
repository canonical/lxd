package importer

import (
	"fmt"
	"strings"

	"github.com/r3labs/diff/v3"
)

type DiagSev string

const (
	Critical DiagSev = "critical"
	Warning  DiagSev = "warning"
	OK       DiagSev = "ok"
)

type Diagnostic struct {
	Msg      string
	Severity DiagSev
}

func NewDiagnostic(change diff.Change, severity DiagSev) Diagnostic {
	msg := ""
	switch {
	case change.Type == diff.UPDATE:
		msg = fmt.Sprintf("Updated from %v to %v in target at %q. ", change.From, change.To, strings.Join(change.Path, "/"))
	case change.Type == diff.DELETE:
		msg = fmt.Sprintf("Deleted %v in target at %q. ", change.From, strings.Join(change.Path, "/"))
	case change.Type == diff.CREATE:
		msg = fmt.Sprintf("Created %v in target at %q. ", change.To, strings.Join(change.Path, "/"))
	}

	if severity == Critical {
		msg += "This change is critical and is not permitted."
	} else if severity == Warning {
		msg += "This change is a warning."
	}

	return Diagnostic{
		Msg:      msg,
		Severity: severity,
	}
}

type Diagnostics []Diagnostic

func (d Diagnostics) String() string {
	s := "DIAGNOSTICS\n"
	fail := false
	for _, diag := range d {
		if diag.Severity == Critical {
			fail = true
		}

		s += fmt.Sprintf("- %s: %s\n", diag.Severity, diag.Msg)
	}

	if fail {
		s += "Plan failed\n"
	} else {
		s += "OK\n"
	}

	return s
}
