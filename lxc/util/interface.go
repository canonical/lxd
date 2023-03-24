package printers

import "io"

// TODO at some point it might be worth discussing a generic object model, so we can use this here instead of 'any'
type ResourcePrinterFunc func(obj any, writer io.Writer) error

func (fn ResourcePrinterFunc) PrintObj(obj any, writer io.Writer) error {
	return fn(obj, writer)
}

type ResourcePrinter interface {
	PrintObj(raw any, writer io.Writer) error
}

type PrintOptions struct {
	CompactMode  bool
	ColumnLabels []string
}
