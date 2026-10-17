//go:build linux

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// beforeHandoff puts back the NIC firmware LLDP flags: ice keeps them
// across boots, so a missed restore leaves the switch without DCBX on the
// next OS.
func TestBeforeHandoffRestoresFirmwareLLDP(t *testing.T) {
	orig := restoreFirmwareLLDP
	t.Cleanup(func() { restoreFirmwareLLDP = orig })
	calls := 0
	restoreFirmwareLLDP = func() (int, error) { calls++; return 1, nil }
	beforeHandoff()
	if calls != 1 {
		t.Fatalf("RestoreFirmwareLLDP calls = %d, want 1", calls)
	}
}

// Every way BOOTy hands the machine on calls beforeHandoff before the
// handoff itself (realm.Reboot, RequestReboot, RequestPowerOff or
// kexec.Execute). The handoffs reboot or kexec, so the test reads main.go
// instead of running them.
func TestHandoffsCallBeforeHandoff(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"reboot": false, "requestReboot": false, "requestPowerOff": false, "doKexecExecute": false}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		if _, ok := want[fn.Name.Name]; !ok {
			continue
		}
		want[fn.Name.Name] = callsBeforeHandoff(fn)
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("%s does not call beforeHandoff() before the handoff", name)
		}
	}
}

// callsBeforeHandoff reports whether a top-level statement of fn calls
// beforeHandoff before the first statement that hands the machine on.
func callsBeforeHandoff(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	for _, stmt := range fn.Body.List {
		if es, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := es.X.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "beforeHandoff" {
					return true
				}
			}
		}
		if handsOn(stmt) {
			return false
		}
	}
	return false
}

// handsOn reports whether n calls realm.Reboot, realm.RequestReboot,
// realm.RequestPowerOff or kexec.Execute.
func handsOn(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return !found
		}
		if pkg, ok := sel.X.(*ast.Ident); ok {
			switch pkg.Name + "." + sel.Sel.Name {
			case "realm.Reboot", "realm.RequestReboot", "realm.RequestPowerOff", "kexec.Execute":
				found = true
			}
		}
		return !found
	})
	return found
}
