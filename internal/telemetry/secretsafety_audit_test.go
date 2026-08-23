/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package telemetry

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Repo-level secret-safety audit (observability spec: "No log line, event,
// metric, condition message, or error string SHALL contain secret payload
// data"). It parses every non-test Go file under internal/ and cmd/ and
// fails when a message-producing call (event emission, Sprintf/Errorf,
// structured log call) references a payload-bearing field: Data,
// StringData, or BinaryData (as a selector or a string index key).
//
// This is a static ratchet, not a proof — it complements the runtime canary
// test in internal/engine (TestReconcile_NoSecretPayloadInMessagesOrEvents)
// and the human checklist in REVIEW_CHECKLIST.md.
func TestNoPayloadFieldsInMessageFormatting(t *testing.T) {
	root := repoRoot(t)
	var violations []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			violations = append(violations, auditFile(t, path)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	for _, v := range violations {
		t.Errorf("payload field referenced in message formatting: %s", v)
	}
}

// messageCallNames are callee method/function names whose arguments become
// user-visible text (events, errors, log lines, format strings).
var messageCallNames = map[string]bool{
	"Event":   true, // record.EventRecorder / events.EventRecorder
	"Eventf":  true,
	"event":   true, // engine's internal event helper
	"Sprintf": true,
	"Errorf":  true,
	"Error":   true, // logr
	"Info":    true, // logr
}

// payloadFieldNames are struct fields / map keys that carry object payload.
var payloadFieldNames = map[string]bool{
	"Data":       true,
	"StringData": true,
	"BinaryData": true,
	"data":       true,
	"stringData": true,
	"binaryData": true,
}

func auditFile(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call)
		if !messageCallNames[name] {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(inner ast.Node) bool {
				switch e := inner.(type) {
				case *ast.SelectorExpr:
					if payloadFieldNames[e.Sel.Name] {
						out = append(out, fset.Position(e.Pos()).String()+" (field "+e.Sel.Name+" in "+name+" call)")
					}
				case *ast.IndexExpr:
					if lit, ok := e.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						key := strings.Trim(lit.Value, `"`)
						if payloadFieldNames[key] {
							out = append(out, fset.Position(e.Pos()).String()+" (index "+lit.Value+" in "+name+" call)")
						}
					}
				}
				return true
			})
		}
		return true
	})
	return out
}

func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source file")
	}
	// internal/telemetry/<this file> -> repo root is two directories up.
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
