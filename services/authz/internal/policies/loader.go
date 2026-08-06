// Package policies loads .cedar files from a directory in lexicographic order.
package policies

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LoadDir reads every *.cedar file in dir, in sorted filename order, and
// concatenates them. Returns the combined bytes and the list of files loaded.
func LoadDir(dir string) ([]byte, []string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("policies: stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("policies: %s not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".cedar" {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) == 0 {
		return nil, nil, errors.New("policies: no .cedar files found")
	}
	sort.Strings(files)

	var buf bytes.Buffer
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return nil, nil, err
		}
		buf.WriteString("// === ")
		buf.WriteString(f)
		buf.WriteString(" ===\n")
		buf.Write(data)
		buf.WriteString("\n")
	}
	return buf.Bytes(), files, nil
}
