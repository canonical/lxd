package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared/i18n"
)

// FormatParser defines the interface for alias format parsers.
type FormatParser interface {
	Parse(data []byte) (map[string]string, error)
	Serialize(aliases map[string]string) ([]byte, error)
	Detect(filename string, data []byte) bool
	Name() string
}

// ParserRegistry manages all available format parsers.
type ParserRegistry struct {
	parsers []FormatParser
}

// NewParserRegistry creates a new registry with default parsers.
func NewParserRegistry() *ParserRegistry {
	return &ParserRegistry{
		parsers: []FormatParser{
			&YAMLParser{},
			&JSONParser{},
			&CSVParser{},
		},
	}
}

// Register adds a new parser to the registry.
func (r *ParserRegistry) GetParser(name string) FormatParser {
	for _, parser := range r.parsers {
		if parser.Name() == name {
			return parser
		}
	}
	return nil
}

// GetParserByExtension returns a parser by file extension.
func (r *ParserRegistry) GetParserByExtension(filename string) FormatParser {
	extension := strings.ToLower(filepath.Ext(filename))
	for _, parser := range r.parsers {
		// Check if this parser supports the extension
		yamlParser, ok := parser.(interface{ SupportedExtensions() []string })
		if ok {
			// For parsers that support multiple extensions
			for _, ext := range yamlParser.SupportedExtensions() {
				if extension == ext {
					return parser
				}
			}
		} else {
			// For parsers with single extension
			if extension == "."+parser.Name() {
				return parser
			}
		}
	}

	return nil
}

// DetectFormat tries to detect the format automatically.
func (r *ParserRegistry) DetectFormat(filename string, data []byte) (FormatParser, error) {
	for _, parser := range r.parsers {
		if parser.Detect(filename, data) {
			return parser, nil
		}
	}
	return nil, fmt.Errorf(i18n.G("unable to detect file format for: %s"), filename)
}

// YAMLParser implements FormatParser for YAML format.
type YAMLParser struct{}

func (p *YAMLParser) Name() string {
	return "yaml"
}

func (p *YAMLParser) SupportedExtensions() []string {
	return []string{".yml", ".yaml"}
}

func (p *YAMLParser) Detect(filename string, data []byte) bool {
	// For stdin, try to parse as YAML to see if it works
	if filename == "" {
		return yaml.Unmarshal(data, &struct{}{}) == nil
	}

	extension := strings.ToLower(filepath.Ext(filename))
	return extension == ".yaml" || extension == ".yml"
}

func (p *YAMLParser) Parse(data []byte) (map[string]string, error) {
	var config struct {
		Aliases map[string]string `yaml:"aliases"`
	}

	err := yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return config.Aliases, nil
}

func (p *YAMLParser) Serialize(aliases map[string]string) ([]byte, error) {
	config := struct {
		Aliases map[string]string `yaml:"aliases"`
	}{
		Aliases: aliases,
	}

	return yaml.Marshal(&config)
}

// JSONParser implements FormatParser for JSON format.
type JSONParser struct{}

func (p *JSONParser) Name() string { return "json" }

func (p *JSONParser) Detect(filename string, data []byte) bool {
	if filename == "" {
		// For stdin, try to parse as JSON to see if it works
		return json.Unmarshal(data, &struct{}{}) == nil
	}

	extension := strings.ToLower(filepath.Ext(filename))
	return extension == ".json"
}

func (p *JSONParser) Parse(data []byte) (map[string]string, error) {
	var config struct {
		Aliases map[string]string `json:"aliases"`
	}

	err := json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return config.Aliases, nil
}

func (p *JSONParser) Serialize(aliases map[string]string) ([]byte, error) {
	config := struct {
		Aliases map[string]string `json:"aliases"`
	}{
		Aliases: aliases,
	}

	data, err := json.MarshalIndent(&config, "", "	")
	if err != nil {
		return nil, err
	}

	// Append newline for prpoer JSON formatting.
	return append(data, '\n'), nil
}

// CSVParser implements FormatParser for CSV format.
type CSVParser struct{}

func (p *CSVParser) Name() string { return "csv" }

func (p *CSVParser) Detect(filename string, data []byte) bool {
	if filename == "" {
		// Simple CSV detection - check if first line has comma and valid structure
		lines := strings.Split(string(data), "\n")
		if len(lines) > 0 && strings.Contains(lines[0], ",") {
			return true
		}

		return false
	}

	extension := strings.ToLower(filepath.Ext(filename))
	return extension == ".csv"
}

func (p *CSVParser) Parse(data []byte) (map[string]string, error) {
	reader := csv.NewReader(strings.NewReader(string(data)))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf(i18n.G("CSV file read failed %v"), err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("%s", i18n.G("Empty csv file"))
	}

	aliases := make(map[string]string)
	for i := 1; i < len(records); i++ {
		if len(records[i]) >= 2 {
			alias := strings.Trim(records[i][0], ",")
			target := strings.Trim(records[i][1], ",")
			if alias != "" && target != "" {
				aliases[alias] = target
			}
		}
	}

	return aliases, nil
}

func (p *CSVParser) Serialize(aliases map[string]string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)

	// Write header
	err := writer.Write([]string{"alias", "command"})
	if err != nil {
		return nil, err
	}

	// Sort aliases for consistent output
	aliasKeys := make([]string, 0, len(aliases))
	for k := range aliases {
		aliasKeys = append(aliasKeys, k)
	}
	// Sort keys alphabetically
	sort.Strings(aliasKeys)

	// Write aliases
	for _, alias := range aliasKeys {
		err := writer.Write([]string{alias, aliases[alias]})
		if err != nil {
			return nil, err
		}
	}

	writer.Flush()
	return buffer.Bytes(), writer.Error()
}

// GetFormatNames initializes parser registry and get available format nanes.
func GetFormatNames() (registry *ParserRegistry, formatNames []string) {
	// Initialize parser registry and get available format names
	registry = NewParserRegistry()
	formatNames = make([]string, len(registry.parsers))
	for i, parser := range registry.parsers {
		formatNames[i] = parser.Name()
	}

	return registry, formatNames
}
