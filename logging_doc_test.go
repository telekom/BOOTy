package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The log message catalog of docs/logging.md sits between these markers.
// TestLoggingDocCatalog regenerates it from the code and fails when the
// doc differs; UPDATE_LOGGING_DOC=1 rewrites the doc instead.
const (
	logCatalogStart = "<!-- log-catalog:start -->"
	logCatalogEnd   = "<!-- log-catalog:end -->"
)

// logMethodLevels maps the slog call names to the level they log at.
var logMethodLevels = map[string]string{
	"Debug": "DEBUG", "DebugContext": "DEBUG",
	"Info": "INFO", "InfoContext": "INFO",
	"Warn": "WARN", "WarnContext": "WARN",
	"Error": "ERROR", "ErrorContext": "ERROR",
}

var slogLevelConsts = map[string]string{
	"LevelDebug": "DEBUG", "LevelInfo": "INFO", "LevelWarn": "WARN", "LevelError": "ERROR",
}

// loggerReceiver matches the expressions that hold a *slog.Logger in BOOTy
// (slog, log, c.Log, o.log, r.logger()). Log and LogAttrs only count on
// them; other types have Log methods too.
var loggerReceiver = regexp.MustCompile(`^slog$|(^|\.)(log|Log|logger|Logger)(\(\))?$`)

type logCall struct {
	level, pkg, msg string
}

// logCalls returns every log call of the shipped code: all non-test Go
// files except test/ and hack/. A call counts when it is a slog level
// method (Debug, Info, ..., Log, LogAttrs) with a string literal message.
func logCalls(t *testing.T) []logCall {
	t.Helper()
	var calls []logCall
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (strings.HasPrefix(d.Name(), ".") || path == "test" || path == "hack" || path == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		strs := fileStrings(file)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if c, ok := logCallOf(t, fset, call, strs); ok {
				c.pkg = filepath.ToSlash(filepath.Dir(path))
				calls = append(calls, c)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go files: %v", err)
	}
	return calls
}

func logCallOf(t *testing.T, fset *token.FileSet, call *ast.CallExpr, strs map[string][]string) (logCall, bool) {
	t.Helper()
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return logCall{}, false
	}
	recv := types.ExprString(sel.X)
	level, msgIdx := logMethodLevels[sel.Sel.Name], 0
	switch {
	case strings.HasSuffix(sel.Sel.Name, "Context"):
		msgIdx = 1
	case sel.Sel.Name == "Log" || sel.Sel.Name == "LogAttrs":
		if !loggerReceiver.MatchString(recv) || len(call.Args) < 3 {
			return logCall{}, false
		}
		if l, ok := call.Args[1].(*ast.SelectorExpr); ok {
			level = slogLevelConsts[l.Sel.Name]
		}
		if level == "" {
			t.Errorf("%s: log call with a level that is not a slog.LevelX constant", fset.Position(call.Pos()))
			return logCall{}, false
		}
		msgIdx = 2
	}
	if level == "" || len(call.Args) <= msgIdx {
		return logCall{}, false
	}
	msg, literal := messageText(call.Args[msgIdx], strs)
	// fs.DirEntry.Info(), http.Error(w, ...) and the like have no
	// string message; a logger with a computed message still counts.
	if !literal && !loggerReceiver.MatchString(recv) {
		return logCall{}, false
	}
	return logCall{level: level, msg: msg}, true
}

// messageText returns the message of a log call. Parts that are not string
// literals become <expression> placeholders; literal is false when the
// message has no literal part.
func messageText(e ast.Expr, strs map[string][]string) (msg string, literal bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if s, err := strconv.Unquote(e.Value); err == nil && e.Kind == token.STRING {
			return s, true
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			x, xl := messageText(e.X, strs)
			y, yl := messageText(e.Y, strs)
			return x + y, xl || yl
		}
	case *ast.Ident:
		// msg := "..." or const msg = "..." in the same file.
		if s := strs[e.Name]; len(s) == 1 {
			return s[0], true
		}
	}
	return "<" + types.ExprString(e) + ">", false
}

// fileStrings maps the names that a file assigns a string literal to (with
// := or in a const or var declaration) to the distinct literals.
func fileStrings(file *ast.File) map[string][]string {
	strs := map[string][]string{}
	add := func(name *ast.Ident, value ast.Expr) {
		if lit, ok := value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && !slices.Contains(strs[name.Name], s) {
				strs[name.Name] = append(strs[name.Name], s)
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						add(id, n.Rhs[i])
					}
				}
			}
		case *ast.ValueSpec:
			if len(n.Names) == len(n.Values) {
				for i, id := range n.Names {
					add(id, n.Values[i])
				}
			}
		}
		return true
	})
	return strs
}

// renderLogCatalog renders the catalog: per level, per package, the
// sorted distinct messages.
func renderLogCatalog(calls []logCall) string {
	byLevel := map[string]map[string][]string{}
	for _, c := range calls {
		if byLevel[c.level] == nil {
			byLevel[c.level] = map[string][]string{}
		}
		if !slices.Contains(byLevel[c.level][c.pkg], c.msg) {
			byLevel[c.level][c.pkg] = append(byLevel[c.level][c.pkg], c.msg)
		}
	}
	var b strings.Builder
	b.WriteString(logCatalogStart + "\n")
	b.WriteString("<!-- Generated by TestLoggingDocCatalog (UPDATE_LOGGING_DOC=1). Do not edit. -->\n")
	for _, level := range []string{"ERROR", "WARN", "INFO", "DEBUG"} {
		pkgs := byLevel[level]
		total := 0
		for _, msgs := range pkgs {
			total += len(msgs)
		}
		fmt.Fprintf(&b, "\n### %s (%d messages)\n", level, total)
		for _, pkg := range slices.Sorted(maps.Keys(pkgs)) {
			fmt.Fprintf(&b, "\n#### %s `%s`\n\n", level, pkg)
			msgs := pkgs[pkg]
			slices.Sort(msgs)
			for _, m := range msgs {
				fmt.Fprintf(&b, "- %s\n", codeSpan(m))
			}
		}
	}
	b.WriteString("\n" + logCatalogEnd)
	return b.String()
}

// codeSpan renders s as a Markdown code span that can hold backticks.
func codeSpan(s string) string {
	if s == "" {
		return "(empty message)"
	}
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

func TestLoggingDocCatalog(t *testing.T) {
	calls := logCalls(t)
	// One call per call form, so a parser change that misses a form fails.
	for _, sentinel := range []logCall{
		{"INFO", ".", "starting BOOTy"},
		{"DEBUG", "pkg/network/dhcp4", "DHCP lease bound"},
		{"WARN", "cmd/booty-esp-sync", "peer EFI system partition is missing, the mirror is degraded; nothing to synchronize"},
		{"ERROR", "pkg/timesync", "refusing to set a plausible clock back on the CAPRF Date header alone"},
	} {
		if !slices.Contains(calls, sentinel) {
			t.Fatalf("log call scan does not find %+v; update logCallOf", sentinel)
		}
	}

	const path = "docs/logging.md"
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	start := strings.Index(string(doc), logCatalogStart)
	end := strings.Index(string(doc), logCatalogEnd)
	if start < 0 || end < start {
		t.Fatalf("%s has no %s ... %s block", path, logCatalogStart, logCatalogEnd)
	}
	got := string(doc[start : end+len(logCatalogEnd)])
	want := renderLogCatalog(calls)
	if got == want {
		return
	}
	if os.Getenv("UPDATE_LOGGING_DOC") == "1" {
		updated := string(doc[:start]) + want + string(doc[end+len(logCatalogEnd):])
		if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	t.Errorf("the log message catalog in %s is out of date with the code.\n"+
		"Run: UPDATE_LOGGING_DOC=1 go test -run TestLoggingDocCatalog .\n"+
		"and review the diff; describe new kinds of messages in the level sections of the doc.%s",
		path, catalogDiff(got, want))
}

// catalogDiff lists the lines that only one side has.
func catalogDiff(got, want string) string {
	var b strings.Builder
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for _, l := range wantLines {
		if !slices.Contains(gotLines, l) {
			b.WriteString("\n+ " + l)
		}
	}
	for _, l := range gotLines {
		if !slices.Contains(wantLines, l) {
			b.WriteString("\n- " + l)
		}
	}
	return b.String()
}
