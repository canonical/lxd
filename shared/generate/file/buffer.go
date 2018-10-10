package file

import (
	"bytes"
	"fmt"
	"go/format"
	"strings"

	"github.com/lxc/lxd/shared/generate/lex"
	"github.com/pkg/errors"
)

// Buffer for accumulating source code output.
type Buffer struct {
	buf *bytes.Buffer
}

// Create a new source code text buffer.
func newBuffer() *Buffer {
	return &Buffer{
		buf: bytes.NewBuffer(nil),
	}
}

// L accumulates a single line of source code.
func (b *Buffer) L(format string, a ...interface{}) {
	fmt.Fprintf(b.buf, format, a...)
	b.N()
}

// N accumulates a single new line.
func (b *Buffer) N() {
	fmt.Fprintf(b.buf, "\n")
}

// Returns the source code to add to the target file.
func (b *Buffer) code() ([]byte, error) {
	code, err := format.Source(b.buf.Bytes())
	if err != nil {
		return nil, errors.Wrap(err, "Can't format generated source code")
	}
	return code, nil
}

func varDeclSliceToString(decls []lex.VarDecl) string {
	parts := []string{}

	for _, decl := range decls {
		parts = append(parts, decl.String())
	}

	return strings.Join(parts, ", ")
}
