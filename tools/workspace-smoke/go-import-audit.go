package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type importRecord struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Alias    string `json:"alias"`
}

func main() {
	inputs := os.Args[1:]
	if len(inputs) > 0 && inputs[0] == "--" {
		inputs = inputs[1:]
	}
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: go-import-audit <directory-or-file> [...]")
		os.Exit(2)
	}

	records, err := auditImports(inputs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go import audit: %v\n", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(records); err != nil {
		fmt.Fprintf(os.Stderr, "go import audit: encode records: %v\n", err)
		os.Exit(1)
	}
}

func auditImports(inputs []string) ([]importRecord, error) {
	files, err := productionGoFiles(inputs)
	if err != nil {
		return nil, err
	}

	records := make([]importRecord, 0)
	fileset := token.NewFileSet()
	for _, filename := range files {
		file, err := parser.ParseFile(fileset, filename, nil, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", filepath.ToSlash(filename), err)
		}
		for _, importSpec := range file.Imports {
			importPath, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return nil, fmt.Errorf(
					"resolve import path in %s: %w",
					filepath.ToSlash(filename),
					err,
				)
			}
			alias := ""
			if importSpec.Name != nil {
				alias = importSpec.Name.Name
			}
			records = append(records, importRecord{
				Filename: filepath.ToSlash(filepath.Clean(filename)),
				Path:     importPath,
				Alias:    alias,
			})
		}
	}

	sort.Slice(records, func(left, right int) bool {
		if records[left].Filename != records[right].Filename {
			return records[left].Filename < records[right].Filename
		}
		if records[left].Path != records[right].Path {
			return records[left].Path < records[right].Path
		}
		return records[left].Alias < records[right].Alias
	})
	return records, nil
}

func productionGoFiles(inputs []string) ([]string, error) {
	files := make(map[string]struct{})
	for _, input := range inputs {
		info, err := os.Stat(input)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", filepath.ToSlash(input), err)
		}
		if !info.IsDir() {
			if isProductionGoFile(input) {
				files[filepath.Clean(input)] = struct{}{}
			}
			continue
		}

		err = filepath.WalkDir(input, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type().IsRegular() && isProductionGoFile(path) {
				files[filepath.Clean(path)] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", filepath.ToSlash(input), err)
		}
	}

	sortedFiles := make([]string, 0, len(files))
	for filename := range files {
		sortedFiles = append(sortedFiles, filename)
	}
	sort.Slice(sortedFiles, func(left, right int) bool {
		return filepath.ToSlash(sortedFiles[left]) < filepath.ToSlash(sortedFiles[right])
	})
	return sortedFiles, nil
}

func isProductionGoFile(filename string) bool {
	return strings.HasSuffix(filename, ".go") && !strings.HasSuffix(filename, "_test.go")
}
