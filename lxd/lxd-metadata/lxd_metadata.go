package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared"

	"gopkg.in/yaml.v2"
)

var (
	globalLxdDocRegex    = regexp.MustCompile(`(?m)lxdmeta:generate\((.*)\)([\S\s]+)\s+---\n([\S\s]+)`)
	lxdDocMetadataRegex  = regexp.MustCompile(`(?m)([^;\s]+)=([^;\s]+)`)
	lxdDocDataRegex      = regexp.MustCompile(`(?m)([\S]+):[\s]+([\S \"\']+)`)
	tpl                  = regexp.MustCompile(`{{(\S*?)}}`)
	compressedExpression = regexp.MustCompile(`([^,{}]+)\{([^}]+)\}`)
)

var mdKeys = []string{"entities", "group", "key"}

// IterableAny is a generic type that represents a type or an iterable container.
type IterableAny interface {
	any | []any
}

// doc is the structure of the JSON file that contains the generated configuration metadata.
type doc struct {
	Configs  map[string]map[string]map[string][]any `json:"configs"`
	Entities json.RawMessage                        `json:"entities"`
}

// detectType detects the type of a string and returns the corresponding value.
func detectType(s string) any {
	i, err := strconv.Atoi(s)
	if err == nil {
		return i
	}

	b, err := strconv.ParseBool(s)
	if err == nil {
		return b
	}

	f, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return f
	}

	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t
	}

	// special characters handling
	if s == "-" {
		return ""
	}

	// If all conversions fail, it's a string
	return s
}

// sortConfigKeys alphabetically sorts the entries by key (config option key) within each config group in an entity.
func sortConfigKeys(allEntries map[string]map[string]map[string][]any) {
	for _, entityValue := range allEntries {
		for _, groupValue := range entityValue {
			configEntries := groupValue["keys"]
			sort.Slice(configEntries, func(i, j int) bool {
				// Get the only key for each map element in the slice
				var keyI, keyJ string

				confI, confJ := configEntries[i].(map[string]any), configEntries[j].(map[string]any)
				for k := range confI {
					keyI = k
					break // There is only one key-value pair in each map
				}

				for k := range confJ {
					keyJ = k
					break // There is only one key-value pair in each map
				}

				// Compare the keys
				return keyI < keyJ
			})
		}
	}
}

// getSortedKeysFromMap returns the keys of a map sorted alphabetically.
func getSortedKeysFromMap[K string, V IterableAny](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// renderString replaces the keys in a string with their corresponding values from a substitution database.
func renderString(db map[string]string, s string) string {
	if db == nil {
		return s
	}

	return tpl.ReplaceAllStringFunc(s, func(match string) string {
		key := strings.Trim(match, "{} ") // Remove the curly braces and any spaces
		val, ok := db[key]
		if ok {
			return val
		}

		return match
	})
}

// expandExpression expands a compressed expression of the form "prefix{part1+part2+part3+...}" into "prefixpart1,prefixpart2,prefixpart3,..."
// It can also work with ...,<string1>,<string2>,<prefix1>{<part1>+<part2>},<string3>,<prefix2>{<part3>+<part4>+...},...
func expandExpression(input string) string {
	result := input
	for _, match := range compressedExpression.FindAllStringSubmatch(input, -1) {
		if len(match) < 3 {
			continue // Skip if the match does not contain at least 3 groups (a 'compressed expression')
		}

		prefix := match[1]
		partsStr := match[2]

		// Split the parts string based on '+'
		parts := strings.Split(partsStr, "+")
		var expandedParts []string
		for _, part := range parts {
			expandedParts = append(expandedParts, prefix+part)
		}

		expandedResult := strings.Join(expandedParts, ",")
		result = strings.Replace(result, match[0], expandedResult, 1)
	}

	return result
}

func parse(path string, outputJSONPath string, excludedPaths []string, substitutionDBPath string) (*doc, error) {
	jsonDoc := &doc{}
	docKeys := make(map[string]struct{}, 0)
	allEntries := make(map[string]map[string]map[string][]any)

	var substitutionRules map[string]string
	if substitutionDBPath != "" {
		// Load the substitution rules
		data, err := os.ReadFile(substitutionDBPath)
		if err != nil {
			return nil, fmt.Errorf("Error reading substitution database: %v", err)
		}

		substitutionRules = make(map[string]string)
		err = yaml.Unmarshal(data, &substitutionRules)
		if err != nil {
			return nil, fmt.Errorf("Error unmarshaling substitution database: %v", err)
		}
	}

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip excluded paths
		if shared.ValueInSlice(path, excludedPaths) {
			if info.IsDir() {
				log.Printf("Skipping excluded directory: %v", path)
				return filepath.SkipDir
			}

			log.Printf("Skipping excluded file: %v", path)
			return nil
		}

		// Only process go files
		if !info.IsDir() && filepath.Ext(path) != ".go" {
			return nil
		}

		// Continue walking if directory
		if info.IsDir() {
			return nil
		}

		// Parse file and create the AST
		var fset = token.NewFileSet()
		var f *ast.File
		f, err = parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		// Loop in comment groups
		for _, cg := range f.Comments {
			s := cg.Text()
			for _, match := range globalLxdDocRegex.FindAllStringSubmatch(s, -1) {
				// check that the match contains the expected number of groups
				if len(match) != 4 {
					continue
				}

				log.Printf("Found lxddoc at %s", fset.Position(cg.Pos()).String())
				metadata := match[1]
				longdesc := match[2]
				data := match[3]
				// process metadata
				metadataMap := make(map[string]string)
				var groupKey string
				var simpleKey string
				for _, mdKVMatch := range lxdDocMetadataRegex.FindAllStringSubmatch(metadata, -1) {
					if len(mdKVMatch) != 3 {
						continue
					}

					mdKey := mdKVMatch[1]
					mdValue := mdKVMatch[2]
					// check that the metadata key is among the expected ones
					if !shared.ValueInSlice(mdKey, mdKeys) {
						continue
					}

					if mdKey == "entities" {
						mdValue = expandExpression(mdValue) // There can be compressed expressions in the 'entities' list
					}

					if mdKey == "group" {
						groupKey = mdValue
					}

					if mdKey == "key" {
						simpleKey = mdValue
					}

					metadataMap[mdKey] = mdValue
				}

				// There can be multiple entities for a given group
				entities := strings.Split(metadataMap["entities"], ",")
				uniqueEntities := make(map[string]struct{})
				// Avoid letting the user define the same entity multiple times per comment
				for _, entity := range entities {
					_, ok := uniqueEntities[entity]
					if ok {
						return fmt.Errorf("Duplicate entity '%s' found at %s", entity, fset.Position(cg.Pos()).String())
					}

					uniqueEntities[entity] = struct{}{}
				}

				for entityKey := range uniqueEntities {
					// Check that this metadata is not already present
					mdKeyHash := fmt.Sprintf("%s/%s/%s", entityKey, groupKey, simpleKey)
					_, ok := docKeys[mdKeyHash]
					if ok {
						return fmt.Errorf("Duplicate key '%s' found at %s", mdKeyHash, fset.Position(cg.Pos()).String())
					}

					docKeys[mdKeyHash] = struct{}{}
				}

				configKeyEntry := make(map[string]any)
				configKeyEntry[metadataMap["key"]] = make(map[string]any)
				configKeyEntry[metadataMap["key"]].(map[string]any)["longdesc"] = renderString(substitutionRules, strings.TrimLeft(longdesc, "\n\t\v\f\r"))
				for _, dataKVMatch := range lxdDocDataRegex.FindAllStringSubmatch(data, -1) {
					if len(dataKVMatch) != 3 {
						continue
					}

					configKeyEntry[metadataMap["key"]].(map[string]any)[dataKVMatch[1]] = detectType(dataKVMatch[2])
				}

				// There can be multiple entities for a given group
				for entityKey := range uniqueEntities {
					_, ok := allEntries[entityKey]
					if ok {
						_, ok := allEntries[entityKey][groupKey]
						if ok {
							_, ok := allEntries[entityKey][groupKey]["keys"]
							if ok {
								allEntries[entityKey][groupKey]["keys"] = append(allEntries[entityKey][groupKey]["keys"], configKeyEntry)
							} else {
								allEntries[entityKey][groupKey]["keys"] = make([]any, 0)
								allEntries[entityKey][groupKey]["keys"] = []any{configKeyEntry}
							}
						} else {
							allEntries[entityKey][groupKey] = make(map[string][]any, 0)
							allEntries[entityKey][groupKey]["keys"] = make([]any, 0)
							allEntries[entityKey][groupKey]["keys"] = []any{configKeyEntry}
						}
					} else {
						allEntries[entityKey] = make(map[string]map[string][]any)
						allEntries[entityKey][groupKey] = make(map[string][]any, 0)
						allEntries[entityKey][groupKey]["keys"] = make([]any, 0)
						allEntries[entityKey][groupKey]["keys"] = []any{configKeyEntry}
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// sort the config keys alphabetically
	sortConfigKeys(allEntries)
	jsonDoc.Configs = allEntries

	cmd := exec.Command("go", "run", "./generate/main.go", "--dry-run")
	cmd.Dir = "./lxd/auth"
	entities, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Error while getting entitlement data: %w", err)
	}

	jsonDoc.Entities = entities
	data, err := json.MarshalIndent(jsonDoc, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("Error while marshaling project documentation: %v", err)
	}

	if outputJSONPath != "" {
		buf := bytes.NewBufferString("")
		_, err = buf.Write(data)
		if err != nil {
			return nil, fmt.Errorf("Error while writing the JSON project documentation: %v", err)
		}

		err := os.WriteFile(outputJSONPath, buf.Bytes(), 0644)
		if err != nil {
			return nil, fmt.Errorf("Error while writing the JSON project documentation: %v", err)
		}
	}

	return jsonDoc, nil
}

func writeDocFile(inputJSONPath, outputTxtPath string) error {
	countMaxBackTicks := func(s string) int {
		count, currCount := 0, 0
		n := len(s)
		for i := 0; i < n; i++ {
			if s[i] == '`' {
				currCount++
				continue
			}

			if currCount > count {
				count = currCount
			}

			currCount = 0
		}

		return count
	}

	specialChars := []string{"", "*", "_", "#", "+", "-", ".", "!", "no", "yes"}

	// read the JSON file which is the source of truth for the generation of the .txt file
	jsonData, err := os.ReadFile(inputJSONPath)
	if err != nil {
		return err
	}

	var jsonDoc doc

	err = json.Unmarshal(jsonData, &jsonDoc)
	if err != nil {
		return err
	}

	sortedEntityKeys := getSortedKeysFromMap(jsonDoc.Configs)
	// create a string buffer
	buffer := bytes.NewBufferString("// Code generated by lxd-metadata; DO NOT EDIT.\n\n")
	for _, entityKey := range sortedEntityKeys {
		entityEntries := jsonDoc.Configs[entityKey]
		sortedGroupKeys := getSortedKeysFromMap(entityEntries)
		for _, groupKey := range sortedGroupKeys {
			groupEntries := entityEntries[groupKey]
			buffer.WriteString(fmt.Sprintf("<!-- config group %s-%s start -->\n", entityKey, groupKey))
			for _, configEntryAny := range groupEntries["keys"] {
				configEntry, ok := configEntryAny.(map[string]any)
				if !ok {
					return fmt.Errorf("Unexpected config entry type (%T) in group %q", configEntryAny, groupKey)
				}

				for configKey, configContentAny := range configEntry {
					// There is only one key-value pair in each map
					kvBuffer := bytes.NewBufferString("")
					var backticksCount int
					var longDescContent string
					configContent, ok := configContentAny.(map[string]any)
					if !ok {
						return fmt.Errorf("Unexpected config content type (%T) for key %q in group %q", configContentAny, configKey, groupKey)
					}

					sortedConfigContentKeys := getSortedKeysFromMap(configContent)
					for _, configEntryContentKey := range sortedConfigContentKeys {
						configContentValue := configContent[configEntryContentKey]
						if configEntryContentKey == "longdesc" {
							configContentString, _ := configContentValue.(string)
							backticksCount = countMaxBackTicks(configContentString)
							longDescContent = configContentString
							continue
						}

						configContentValueStr, ok := configContentValue.(string)
						if ok {
							if (strings.HasSuffix(configContentValueStr, "`") && strings.HasPrefix(configContentValueStr, "`")) || shared.ValueInSlice(configContentValueStr, specialChars) {
								configContentValueStr = fmt.Sprintf("\"%s\"", configContentValueStr)
							}
						} else {
							switch configEntryContentTyped := configContentValue.(type) {
							case int, float64, bool:
								configContentValueStr = fmt.Sprint(configEntryContentTyped)
							case time.Time:
								configContentValueStr = fmt.Sprint(configEntryContentTyped.Format(time.RFC3339))
							}
						}

						var quoteFormattedValue string
						if strings.Contains(configContentValueStr, `"`) {
							if strings.HasPrefix(configContentValueStr, `"`) && strings.HasSuffix(configContentValueStr, `"`) {
								for i, s := range configContentValueStr[1 : len(configContentValueStr)-1] {
									if s == '"' {
										_ = strings.Replace(configContentValueStr, `"`, `\"`, i)
									}
								}
								quoteFormattedValue = configContentValueStr
							} else {
								quoteFormattedValue = strings.ReplaceAll(configContentValueStr, `"`, `\"`)
							}
						} else {
							quoteFormattedValue = fmt.Sprintf("\"%s\"", configContentValueStr)
						}

						kvBuffer.WriteString(
							fmt.Sprintf(
								":%s: %s\n",
								configEntryContentKey,
								quoteFormattedValue,
							),
						)
					}

					if backticksCount < 3 {
						buffer.WriteString(
							fmt.Sprintf("```{config:option} %s %s-%s\n%s%s\n```\n\n",
								configKey,
								entityKey,
								groupKey,
								kvBuffer.String(),
								strings.TrimLeft(longDescContent, "\n"),
							))
					} else {
						configQuotes := strings.Repeat("`", backticksCount+1)
						buffer.WriteString(
							fmt.Sprintf("%s{config:option} %s %s-%s\n%s%s\n%s\n\n",
								configQuotes,
								configKey,
								entityKey,
								groupKey,
								kvBuffer.String(),
								strings.TrimLeft(longDescContent, "\n"),
								configQuotes,
							))
					}
				}
			}

			buffer.WriteString(fmt.Sprintf("<!-- config group %s-%s end -->\n", entityKey, groupKey))
		}
	}

	entities := make(map[string][]map[string]string)
	err = json.Unmarshal(jsonDoc.Entities, &entities)
	if err != nil {
		return err
	}

	sortedEntityNames := make([]string, 0, len(entities))
	for entityName := range entities {
		sortedEntityNames = append(sortedEntityNames, entityName)
	}

	sort.Strings(sortedEntityNames)

	for _, entityName := range sortedEntityNames {
		entitlements := entities[entityName]
		buffer.WriteString(fmt.Sprintf("<!-- entity group %s start -->\n", entityName))
		for _, entitlement := range entitlements {
			buffer.WriteString(fmt.Sprintf("`%s`\n", entitlement["name"]))
			buffer.WriteString(fmt.Sprintf(": %s\n\n", entitlement["description"]))
		}

		buffer.WriteString("\n")
		buffer.WriteString(fmt.Sprintf("<!-- entity group %s end -->\n", entityName))
	}

	err = os.WriteFile(outputTxtPath, buffer.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("Error while writing the Markdown project documentation: %v", err)
	}

	return nil
}
