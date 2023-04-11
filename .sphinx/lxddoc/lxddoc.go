package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Vars
var (
	regexpDocCode       = regexp.MustCompile("^\\([\\w \\._\\+\\-@]+\\)")
	regexSphinxTemplate = regexp.MustCompile("{{\\s{0,}:lxddoc\\((.*)\\)\\s{0,}}}")

	commentIdentifiers = []string{":lxddoc", ":LXDDOC"}
)

// DocCode represents a docCode
type DocCode string

// Comment represents a comment
type Comment struct {
	Message []string
}

// Extract walks through an input path and extracts comments from all files it encounters
func Extract(path string, excludedPaths ...string) (map[DocCode]*Comment, error) {
	comments := make(map[DocCode]*Comment)
	err := extract(path, comments, excludedPaths...)
	if err != nil {
		return nil, err
	}

	return comments, nil
}

func extract(path string, comments map[DocCode]*Comment, excludedPaths ...string) error {
	return filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		// Process error
		if err != nil {
			return err
		}

		// Skip excluded paths
		for _, p := range excludedPaths {
			if p == path {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip vendor and all directories beginning with a .
		if info.IsDir() && (info.Name() == "vendor" || (len(info.Name()) > 1 && info.Name()[0] == '.')) {
			return filepath.SkipDir
		}

		// Only process go files
		if !info.IsDir() && filepath.Ext(path) != ".go" {
			return nil
		}

		// Everything is fine here, extract if path is a file
		if !info.IsDir() {
			if err = extractFile(path, comments); err != nil {
				return err
			}
		}
		return nil
	})
}

func extractFile(filename string, comments map[DocCode]*Comment) (err error) {
	// Parse file and create the AST
	var fset = token.NewFileSet()
	var f *ast.File
	if f, err = parser.ParseFile(fset, filename, nil, parser.ParseComments); err != nil {
		return
	}

	// Loop in comment groups
	for _, cg := range f.Comments {
		// Loop in comments
		var comment *Comment
		var CommentFound bool
		for _, c := range cg.List {
			// Loop in lines
			for i, l := range strings.Split(c.Text, "\n") {
				// Init text
				var com = strings.TrimSpace(l)
				if strings.HasPrefix(com, "//") || strings.HasPrefix(com, "/*") || strings.HasPrefix(com, "*/") {
					com = strings.TrimSpace(com[2:])
				}

				// Comment found
				if length, isComment := isCommentIdentifier(com); isComment {
					// Init comment
					comment = &Comment{}
					com = strings.TrimSpace(com[length:])
					if strings.HasPrefix(com, ":") {
						com = strings.TrimLeft(com, ":")
						com = strings.TrimSpace(com)
					}

					// Look for docCode
					docCode := regexpDocCode.FindString(com)
					if docCode != "" {
						com = strings.TrimSpace(com[len(docCode):])
						if strings.HasPrefix(com, ":") {
							com = strings.TrimLeft(com, ":")
							com = strings.TrimSpace(com)
						}
						docCode = docCode[1 : len(docCode)-1]
					} else {
						fmt.Println("WARNING: no docCode found for comment", com, "in file", filename, "line", fset.Position(c.Pos()).Line+i)
						continue
					}

					// Append text
					comment.Message = append(comment.Message, com)
					comments[DocCode(docCode)] = comment
					CommentFound = true
				} else if CommentFound && len(com) > 0 {
					comment.Message = append(comment.Message, com)
				} else {
					CommentFound = false
				}
			}
		}
	}
	return
}

func isCommentIdentifier(s string) (int, bool) {
	for _, indent := range commentIdentifiers {
		if strings.HasPrefix(strings.ToUpper(s), indent) {
			return len(indent), true
		}
	}
	return 0, false
}

func InsertTemplate(templateFolder string, comments map[DocCode]*Comment) error {
	visit := func(path string, di fs.DirEntry, err error) error {
		if di.IsDir() {
			return nil
		}

		if filepath.Ext(path) != ".md" {
			return nil
		}

		read, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}

		matches := regexSphinxTemplate.FindAllStringSubmatch(string(read), -1)
		for _, match := range matches {
			if len(match) != 2 {
				return fmt.Errorf("invalid match: %v", match)
			}

			docCode := match[1]
			comment, ok := comments[DocCode(docCode)]
			if !ok {
				return fmt.Errorf("docCode %s not found", docCode)
			}

			// Replace the template with the comment
			newContents := strings.Replace(string(read), match[0], strings.Join(comment.Message, ""), -1)
			err = ioutil.WriteFile(path, []byte(newContents), 0)
			if err != nil {
				panic(err)
			}
		}

		return nil
	}

	return filepath.WalkDir(templateFolder, visit)
}
