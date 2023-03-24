package printers

import (
	"encoding/json"
	"io"
)

type jsonPrinter struct{}

func NewJSONPrinter() ResourcePrinter {
	return &jsonPrinter{}
}

func (p *jsonPrinter) PrintObj(obj any, writer io.Writer) error {
	data, err := json.MarshalIndent(obj, "", "    ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = writer.Write(data)
	return err
}
