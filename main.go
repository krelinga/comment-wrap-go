// comment-wrap reformats Go source files so that // comment lines do not
// exceed a given line-length limit.  It accepts the same package-pattern
// syntax as `go test` (e.g. `.`, `./...`).
//
// Usage:
//
//	comment-wrap [-limit N] [packages...]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/krelinga/comment-wrap-go/internal/wrap"
	"golang.org/x/tools/go/packages"
)

func main() {
	limit := flag.Int("limit", 100, "maximum line length for comment lines")
	flag.Parse()

	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Mode: packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		log.Fatalf("loading packages: %v", err)
	}

	var failed bool
	for _, pkg := range pkgs {
		allFiles := make([]string, 0, len(pkg.GoFiles)+len(pkg.OtherFiles))
		allFiles = append(allFiles, pkg.GoFiles...)
		// OtherFiles contains test files not in GoFiles in some load modes;
		// for NeedFiles the Go files are already split into GoFiles /
		// IgnoredFiles.  We include IgnoredFiles so build-tagged files are
		// also reformatted.
		allFiles = append(allFiles, pkg.IgnoredFiles...)

		for _, path := range allFiles {
			if err := processFile(path, *limit); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				failed = true
			}
		}
	}
	if failed {
		os.Exit(1)
	}
}

// processFile reads path, wraps any long comment lines, and writes the result
// back in place using an atomic rename.
func processFile(path string, limit int) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	out, err := wrap.File(src, limit)
	if err != nil {
		return err
	}

	if string(out) == string(src) {
		return nil // nothing changed
	}

	// Write atomically: temp file in the same directory, then rename.
	tmp, err := os.CreateTemp(dirOf(path), ".comment-wrap-*.go.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// Clean up temp file on any failure path.
		os.Remove(tmpName)
	}()

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

// dirOf returns the directory portion of a file path.
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
