package printers

import (
	"encoding/csv"
	"io"
)

type csvPrinter struct{}

func NewCSVPrinter() ResourcePrinter {
	return &csvPrinter{}
}

func (p *csvPrinter) PrintObj(obj any, writer io.Writer) error {
	w := csv.NewWriter(writer)
	data, err := mustConvertToSliceOfSlices(obj)
	if err != nil {
		return err
	}
	err = w.WriteAll(data)
	if err != nil {
		return err
	}

	return w.Error()
}
