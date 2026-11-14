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

// logMessageAllowlist names the functions ("<file>:<function>") whose log
// calls may have a computed message that the scan can not list. Keep it
// empty: prefer literal messages, or a wrapper whose message is one
// parameter (see logWrapper).
var logMessageAllowlist = map[string]string{}

// logWrapper is a function whose log message is built from one of its
// parameters, like logStepFailure(msg, ...). Its log calls are listed once
// per call site, with the literal that the call site passes.
type logWrapper struct {
	param    int
	level    string
	template string // the message with %s for the parameter
}

type goFile struct {
	path, pkg string
	file      *ast.File
}

// shippedGoFiles parses the non-test Go files of the shipped code: all
// except test/ and hack/.
func shippedGoFiles(t *testing.T, fset *token.FileSet) []goFile {
	t.Helper()
	var files []goFile
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
		files = append(files, goFile{path: path, pkg: filepath.ToSlash(filepath.Dir(path)), file: file})
		return nil
	})
	if err != nil {
		t.Fatalf("scan Go files: %v", err)
	}
	return files
}

// eachCall calls f for every call expression of the file with its
// enclosing function (nil outside functions).
func eachCall(file *ast.File, f func(fn *ast.FuncDecl, call *ast.CallExpr)) {
	for _, decl := range file.Decls {
		fn, _ := decl.(*ast.FuncDecl)
		ast.Inspect(decl, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				f(fn, call)
			}
			return true
		})
	}
}

// logCalls returns every log call of the shipped code: each slog level
// method (Debug, Info, ..., Log, LogAttrs) with its message, and each call
// of a logWrapper. A message that the scan can not resolve fails the test.
func logCalls(t *testing.T) []logCall {
	t.Helper()
	fset := token.NewFileSet()
	files := shippedGoFiles(t, fset)
	var calls []logCall
	wrappers := map[string][]logWrapper{} // "<pkg>.<function>"
	for _, f := range files {
		eachCall(f.file, func(fn *ast.FuncDecl, call *ast.CallExpr) {
			level, msgExpr, ok := logCallOf(t, fset, call)
			if !ok {
				return
			}
			display, template, holes := messageText(msgExpr, localStrings(f.file, fn))
			switch {
			case len(holes) == 0:
				calls = append(calls, logCall{level, f.pkg, display})
			case len(holes) == 1 && paramIndex(fn, holes[0]) >= 0:
				key := f.pkg + "." + fn.Name.Name
				wrappers[key] = append(wrappers[key], logWrapper{paramIndex(fn, holes[0]), level, template})
			case fn != nil && logMessageAllowlist[f.path+":"+fn.Name.Name] != "":
				calls = append(calls, logCall{level, f.pkg, display})
			default:
				t.Errorf("%s: log message %q is not a literal, a local string constant or one parameter of a wrapper; docs/logging.md can not list it",
					fset.Position(call.Pos()), display)
			}
		})
	}
	for _, f := range files {
		eachCall(f.file, func(fn *ast.FuncDecl, call *ast.CallExpr) {
			var name string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			for _, w := range wrappers[f.pkg+"."+name] {
				if w.param >= len(call.Args) {
					continue
				}
				msg, _, holes := messageText(call.Args[w.param], localStrings(f.file, fn))
				if len(holes) > 0 {
					t.Errorf("%s: log wrapper %s gets a computed message %q; pass a literal", fset.Position(call.Pos()), name, msg)
					continue
				}
				calls = append(calls, logCall{w.level, f.pkg, fmt.Sprintf(w.template, msg)})
			}
		})
	}
	return calls
}

// logCallOf returns the level and the message argument of a slog call.
func logCallOf(t *testing.T, fset *token.FileSet, call *ast.CallExpr) (level string, msg ast.Expr, ok bool) {
	t.Helper()
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil, false
	}
	recv := types.ExprString(sel.X)
	level, msgIdx := logMethodLevels[sel.Sel.Name], 0
	switch {
	case strings.HasSuffix(sel.Sel.Name, "Context"):
		msgIdx = 1
	case sel.Sel.Name == "Log" || sel.Sel.Name == "LogAttrs":
		if len(call.Args) < 3 {
			return "", nil, false
		}
		if !loggerReceiver.MatchString(recv) {
			t.Errorf("%s: %s.%s on a receiver that the scan does not know as a logger; extend loggerReceiver",
				fset.Position(call.Pos()), recv, sel.Sel.Name)
			return "", nil, false
		}
		if l, ok := call.Args[1].(*ast.SelectorExpr); ok {
			level = slogLevelConsts[l.Sel.Name]
		}
		if level == "" {
			t.Errorf("%s: log call with a level that is not a slog.LevelX constant", fset.Position(call.Pos()))
			return "", nil, false
		}
		msgIdx = 2
	}
	if level == "" || len(call.Args) <= msgIdx {
		return "", nil, false
	}
	// fs.DirEntry.Info(), http.Error(w, ...) and the like have no string
	// message; a logger with a computed message still counts.
	if lit, isLit := call.Args[msgIdx].(*ast.BasicLit); (!isLit || lit.Kind != token.STRING) && !loggerReceiver.MatchString(recv) {
		return "", nil, false
	}
	return level, call.Args[msgIdx], true
}

// messageText resolves a message expression: string literals, local string
// constants and + concatenation. display shows each other part as
// <expression>; template has %s there; holes are those parts.
func messageText(e ast.Expr, strs map[string][]string) (display, template string, holes []ast.Expr) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if s, err := strconv.Unquote(e.Value); err == nil && e.Kind == token.STRING {
			return s, strings.ReplaceAll(s, "%", "%%"), nil
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			xd, xt, xh := messageText(e.X, strs)
			yd, yt, yh := messageText(e.Y, strs)
			return xd + yd, xt + yt, append(xh, yh...)
		}
	case *ast.Ident:
		if s := strs[e.Name]; len(s) == 1 {
			return s[0], strings.ReplaceAll(s[0], "%", "%%"), nil
		}
	}
	return "<" + types.ExprString(e) + ">", "%s", []ast.Expr{e}
}

// paramIndex returns the index of the parameter that e names in fn, or -1.
func paramIndex(fn *ast.FuncDecl, e ast.Expr) int {
	id, ok := e.(*ast.Ident)
	if !ok || fn == nil {
		return -1
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == id.Name {
				return i
			}
			i++
		}
		if len(field.Names) == 0 {
			i++
		}
	}
	return -1
}

// localStrings maps the names that the enclosing function assigns a string
// literal to (:=, =, const, var), and the file-level constants, to their
// distinct literals. A name with more than one literal does not resolve.
func localStrings(file *ast.File, fn *ast.FuncDecl) map[string][]string {
	strs := map[string][]string{}
	add := func(name *ast.Ident, value ast.Expr) {
		if lit, ok := value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && !slices.Contains(strs[name.Name], s) {
				strs[name.Name] = append(strs[name.Name], s)
			}
		}
	}
	collect := func(n ast.Node) bool {
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
	}
	for _, decl := range file.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.CONST {
			ast.Inspect(gd, collect)
		}
	}
	if fn != nil && fn.Body != nil {
		ast.Inspect(fn.Body, collect)
	}
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
		{"WARN", "pkg/disk", "cannot zero the device edge"},
		{"INFO", "pkg/disk", "cannot zero the device edge: tool not available"},
		{"ERROR", ".", "a/b preserveExisting requires kexec; refusing normal reboot because firmware boot state still points at the active slot"},
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
