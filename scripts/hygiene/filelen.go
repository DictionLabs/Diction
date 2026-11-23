// filelen.go <module-dir>: reports .go files over the length limit: non-test files
// 500 code lines, _test.go files 1000. Blank and comment-only lines are not counted.
// Exempt: internal/evalcases/ and text-data files matching (_cases|_words|prompts).go.
// Output: file: N code lines (limit L), then a TOTAL line. Exit 1 if anything is found.
package main

import (
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxCode = 500
	maxTest = 1000
)

var dataFile = regexp.MustCompile(`(_cases|_words|prompts)\.go$`)

func codeLineCount(src []byte, path string) int {
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	lines := map[int]bool{}
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return len(lines)
		}
		start := file.Line(pos)
		end := start
		if tok == token.STRING {
			end = start + strings.Count(lit, "\n")
		}
		for l := start; l <= end; l++ {
			lines[l] = true
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run filelen.go <module-dir>")
		os.Exit(2)
	}
	root := os.Args[1]
	var out []string
	tracked := trackedFiles(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() {
			n := d.Name()
			if path != root && (n == "vendor" || n == "node_modules" || strings.HasPrefix(n, ".")) {
				return filepath.SkipDir
			}
			if strings.Contains(slash+"/", "internal/evalcases/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || dataFile.MatchString(slash) || !tracked[filepath.Join(path)] {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		limit := maxCode
		if strings.HasSuffix(path, "_test.go") {
			limit = maxTest
		}
		if n := codeLineCount(src, path); n > limit {
			out = append(out, fmt.Sprintf("%s: %d code lines (limit %d)", path, n, limit))
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sort.Strings(out)
	for _, l := range out {
		fmt.Println(l)
	}
	fmt.Printf("TOTAL filelen=%d\n", len(out))
	if len(out) > 0 {
		os.Exit(1)
	}
}

// trackedFiles returns the set of git-tracked files under root (paths joined to root),
// so untracked stray copies never skew the result.
func trackedFiles(root string) map[string]bool {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "git ls-files failed in", root+":", err)
		os.Exit(2)
	}
	set := map[string]bool{}
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			set[filepath.Join(root, f)] = true
		}
	}
	return set
}
