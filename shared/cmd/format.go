package cmd

import (
	"strings"
)

// FormatSection properly indents a text section.
func FormatSection(header string, content string) string {
	var out strings.Builder

	// Add section header
	if header != "" {
		out.WriteString(header + ":\n")
	}

	// Indent the content
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line != "" {
			out.WriteString("  ")
		}

		if header == "" && i == len(lines)-1 {
			// Don't add newline when rendering partial section
			out.WriteString(line)
		} else {
			out.WriteString(line + "\n")
		}
	}

	if header != "" {
		// Section separator (when rendering a full section
		out.WriteString("\n")
	}

	return out.String()
}
