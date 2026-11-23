// comments.go <module-dir>: comment-style checker.
// Rules: multiline (comment group longer than 1 line), long-line (comment line over 100
// characters), nolint (the string "nolint" in any comment). Exempt: compiler directives,
// cgo preambles, and whole generated files ("Code generated ... DO NOT EDIT.").
// Exit 1 if anything is found.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxLine = 100

var generated = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

func isDirective(text string) bool {
	for _, p := range []string{"//go:", "//export ", "//line ", "//extern ", "// +build", "//+build"} {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

type finding struct {
	file string
	line int
	rule string
	msg  string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run comments.go <module-dir>")
		os.Exit(2)
	}
	root := os.Args[1]
	var found []finding
	tracked := trackedFiles(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if path != root && (n == "vendor" || n == "node_modules" || strings.HasPrefix(n, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && tracked[filepath.Join(path)] {
			found = append(found, checkFile(root, path)...)
		}
		return nil
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
	totals := map[string]int{}
	for _, f := range found {
		fmt.Printf("%s:%d: %s: %s\n", f.file, f.line, f.rule, f.msg)
		totals[f.rule]++
	}
	fmt.Printf("TOTAL multiline=%d long-line=%d nolint=%d all=%d\n",
		totals["multiline"], totals["long-line"], totals["nolint"], len(found))
	if len(found) > 0 {
		os.Exit(1)
	}
}

type piece struct {
	line, endLine int
	text          string
}

func checkFile(root, path string) []finding {
	rel, _ := filepath.Rel(root, path)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return []finding{{rel, 1, "parse", err.Error()}}
	}
	cgo := cgoPreambles(fset, f)
	var out []finding
	var run []piece
	flush := func() {
		if len(run) == 0 {
			return
		}
		lines := run[len(run)-1].endLine - run[0].line + 1
		if lines > 1 {
			out = append(out, finding{rel, run[0].line, "multiline", fmt.Sprintf("comment block of %d lines", lines)})
		}
		run = nil
	}
	for _, cg := range f.Comments {
		if len(cg.List) > 0 && generated.MatchString(cg.List[0].Text) && cg.Pos() < f.Package {
			return nil
		}
	}
	for _, cg := range f.Comments {
		if cgo[cg] {
			continue
		}
		for _, c := range cg.List {
			start, end := fset.Position(c.Pos()), fset.Position(c.End())
			if strings.Contains(c.Text, "nolint") {
				out = append(out, finding{rel, start.Line, "nolint", "nolint is forbidden"})
			}
			if isDirective(c.Text) {
				flush()
				continue
			}
			for i, l := range strings.Split(c.Text, "\n") {
				if n := utf8.RuneCountInString(strings.TrimRight(l, "\r")); n > maxLine {
					out = append(out, finding{rel, start.Line + i, "long-line", fmt.Sprintf("comment line of %d chars", n)})
				}
			}
			if len(run) > 0 && start.Line > run[len(run)-1].endLine+1 {
				flush()
			}
			run = append(run, piece{start.Line, end.Line, c.Text})
		}
		flush()
	}
	return out
}

func cgoPreambles(fset *token.FileSet, f *ast.File) map[*ast.CommentGroup]bool {
	m := map[*ast.CommentGroup]bool{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, s := range gd.Specs {
			if is, ok := s.(*ast.ImportSpec); ok && is.Path.Value == `"C"` {
				if is.Doc != nil {
					m[is.Doc] = true
				}
				if gd.Doc != nil {
					m[gd.Doc] = true
				}
			}
		}
	}
	return m
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
