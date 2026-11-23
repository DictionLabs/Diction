// privrefs.go <module-dir>: finds private-infrastructure references in code that ships publicly.
// Scans comments and string literals of every .go file under core/ (subpackages included); a module
// whose root is package main (the public gateway, no cmd/gateway) is scanned whole, subdirectories too.
// In _test.go files only path-like patterns count; other files use all patterns.
// Exit 1 on any hit.
package main

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`\.claude/`),
	regexp.MustCompile(`(?i)bow/`),
	regexp.MustCompile(`(?i)plans/`),
}

const pathLikePatterns = 3

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run privrefs.go <module-dir>")
		os.Exit(2)
	}
	root := os.Args[1]
	var files []string
	tracked := trackedFiles(root)
	walkRoot := filepath.Join(root, "core")
	if _, err := os.Stat(filepath.Join(root, "cmd", "gateway")); err != nil {
		walkRoot = root
	}
	_ = filepath.WalkDir(walkRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && (d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".go") && tracked[filepath.Join(p)] {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	hits := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		file := fset.AddFile(f, -1, len(src))
		var s scanner.Scanner
		s.Init(file, src, nil, scanner.ScanComments)
		rel, _ := filepath.Rel(root, f)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok != token.COMMENT && tok != token.STRING {
				continue
			}
			line := fset.Position(pos).Line
			active := patterns
			if strings.HasSuffix(f, "_test.go") {
				active = patterns[:pathLikePatterns]
			}
			for i, l := range strings.Split(lit, "\n") {
				for _, re := range active {
					if m := re.FindString(l); m != "" {
						fmt.Printf("%s:%d: private reference %q\n", rel, line+i, m)
						hits++
					}
				}
			}
		}
	}
	fmt.Printf("TOTAL private-refs=%d\n", hits)
	if hits > 0 {
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
