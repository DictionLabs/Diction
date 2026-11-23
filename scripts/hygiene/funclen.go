// funclen.go <module-dir>: reports every non-test function declaration AND function literal
// whose body spans more than 80 lines, not counting blank and comment-only lines.
// Output: file:line: name N lines, then a TOTAL line. Exit 1 if anything is found.
// Skipped: _test.go files and internal/evalcases/ (case data tables, data exclusion,
// the same path golangci-lint excludes from funlen).
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const maxLines = 80

type finding struct {
	file string
	line int
	name string
	n    int
}

func codeLines(src []byte, fset *token.FileSet, file *token.File) map[int]bool {
	lines := map[int]bool{}
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return lines
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

func count(fset *token.FileSet, body *ast.BlockStmt, code map[int]bool) int {
	from, to := fset.Position(body.Lbrace).Line, fset.Position(body.Rbrace).Line
	n := 0
	for l := from; l <= to; l++ {
		if code[l] {
			n++
		}
	}
	return n
}

func scanFile(path string) ([]finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	code := codeLines(src, fset, fset.File(f.Pos()))
	var out []finding
	add := func(pos token.Pos, name string, body *ast.BlockStmt) {
		if body == nil {
			return
		}
		if n := count(fset, body, code); n > maxLines {
			out = append(out, finding{path, fset.Position(pos).Line, name, n})
		}
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		add(fd.Pos(), fd.Name.Name, fd.Body)
		ast.Inspect(fd, func(n ast.Node) bool {
			if fl, ok := n.(*ast.FuncLit); ok {
				add(fl.Pos(), fmt.Sprintf("%s.func@%d", fd.Name.Name, fset.Position(fl.Pos()).Line), fl.Body)
			}
			return true
		})
	}
	return out, nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run funclen.go <module-dir>")
		os.Exit(2)
	}
	root := os.Args[1]
	var found []finding
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || !tracked[filepath.Join(path)] {
			return nil
		}
		fs, err := scanFile(path)
		found = append(found, fs...)
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	for _, f := range found {
		fmt.Printf("%s:%d: %s %d lines\n", f.file, f.line, f.name, f.n)
	}
	fmt.Printf("TOTAL funclen=%d\n", len(found))
	if len(found) > 0 {
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
