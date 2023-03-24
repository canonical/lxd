package printers

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v2"
)

type yamlPrinter struct{}

func NewYAMLPrinter() ResourcePrinter {
	return &yamlPrinter{}
}

func (p *yamlPrinter) PrintObj(obj any, writer io.Writer) error {
	output, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	if strings.TrimRight(string(output), "\n") == "null" {
		fmt.Fprint(writer, "")
	} else {
		_, err = fmt.Fprint(writer, string(output))
	}
	return err
}
