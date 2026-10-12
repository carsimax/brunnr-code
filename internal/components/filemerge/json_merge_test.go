package filemerge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPermissionOverlayNewWildcardCannotOverrideExistingDeny(t *testing.T) {
	got, err := MergeJSONObjects([]byte(`{"permission":{"bash":{"ssh*":"deny"}}}`), []byte(`{"permission":{"bash":{"*":"allow"}}}`))
	if err != nil || !strings.Contains(strings.Join(strings.Fields(string(got)), ""), `"bash":{"*":"allow","ssh*":"deny"}`) {
		t.Fatalf("new wildcard changed the effective SSH deny: %s, %v", got, err)
	}
}

func TestPermissionOverlayCannotReplaceRestrictionsWithScalarAllow(t *testing.T) {
	for _, base := range []string{`{"permission":{"bash":{"ssh *":"deny"}}}`, `{"agent":{"custom":{"permission":{"bash":"deny"}}}}`} {
		got, err := MergeJSONObjects([]byte(base), []byte(`{"permission":"allow","agent":{"custom":{"permission":"allow"}}}`))
		if err != nil || !strings.Contains(string(got), `"deny"`) {
			t.Fatalf("scalar overlay erased a restriction: %s, %v", got, err)
		}
	}
}

func TestMergeJSONObjectsRecursively(t *testing.T) {
	base := []byte(`{"plugins":["a"],"settings":{"theme":"default","flags":{"x":true}}}`)
	overlay := []byte(`{"settings":{"theme":"custom","flags":{"y":true}},"extra":1}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal merged json error = %v", err)
	}

	settings := got["settings"].(map[string]any)
	flags := settings["flags"].(map[string]any)

	if settings["theme"] != "custom" {
		t.Fatalf("theme = %v", settings["theme"])
	}

	if flags["x"] != true || flags["y"] != true {
		t.Fatalf("flags = %#v", flags)
	}

	plugins := got["plugins"].([]any)
	if len(plugins) != 1 || plugins[0] != "a" {
		t.Fatalf("plugins = %#v", plugins)
	}
}

func TestMergeJSONObjectsSupportsJSONCBase(t *testing.T) {
	base := []byte(`{
	  // VS Code-style comments and trailing commas
	  "editor.fontSize": 14,
	  "files.exclude": {
	    "**/.git": true,
	  },
	}`)
	overlay := []byte(`{"chat.tools.autoApprove": true}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal merged json error = %v", err)
	}

	autoApprove, ok := got["chat.tools.autoApprove"].(bool)
	if !ok || !autoApprove {
		t.Fatalf("chat.tools.autoApprove = %#v", got["chat.tools.autoApprove"])
	}

	if got["editor.fontSize"] != float64(14) {
		t.Fatalf("editor.fontSize = %v", got["editor.fontSize"])
	}
}

func TestMergeJSONObjectsMalformedBaseReturnsOverlayOnly(t *testing.T) {
	// Real user machines (e.g. Windows) may have a malformed ~/.cursor/mcp.json.
	// The installer should recover by treating the broken base as {} and continuing.
	tests := []struct {
		name    string
		base    []byte
		overlay []byte
		wantKey string
	}{
		{
			name:    "base starting with letter",
			base:    []byte(`allow: all`),
			overlay: []byte(`{"mcpServers": {"context7": {"type": "remote"}}}`),
			wantKey: "mcpServers",
		},
		{
			name:    "unclosed json object",
			base:    []byte(`{"ok": true`),
			overlay: []byte(`{"chat.tools.autoApprove": true}`),
			wantKey: "chat.tools.autoApprove",
		},
		{
			name:    "arbitrary text",
			base:    []byte(`a`),
			overlay: []byte(`{"servers": {"engram": {"command": "engram"}}}`),
			wantKey: "servers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := MergeJSONObjects(tt.base, tt.overlay)
			if err != nil {
				t.Fatalf("MergeJSONObjects() error = %v; want nil (malformed base should be treated as {})", err)
			}

			var got map[string]any
			if err := json.Unmarshal(merged, &got); err != nil {
				t.Fatalf("merged result is not valid JSON: %v", err)
			}

			if _, ok := got[tt.wantKey]; !ok {
				t.Fatalf("merged result missing key %q from overlay; got keys: %v", tt.wantKey, got)
			}
		})
	}
}

// ─── __replace__ sentinel tests ───────────────────────────────────────────────

func TestMergeJSONObjectsReplaceSentinelErasesBaseKeys(t *testing.T) {
	base := []byte(`{"mcp":{"engram":{"command":"/opt/homebrew/bin/engram","args":["mcp","--tools=agent"],"type":"local"}}}`)
	overlay := []byte(`{"mcp":{"engram":{"__replace__":{"command":["/opt/homebrew/bin/engram","mcp","--tools=agent"],"type":"local"}}}}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal merged error = %v", err)
	}

	mcp := got["mcp"].(map[string]any)
	eng := mcp["engram"].(map[string]any)

	// args must be gone
	if _, ok := eng["args"]; ok {
		t.Fatalf("engram still has 'args' after __replace__; got: %v", eng)
	}
	// command must be an array
	cmd, ok := eng["command"].([]any)
	if !ok {
		t.Fatalf("engram command is not an array; got: %T = %v", eng["command"], eng["command"])
	}
	if len(cmd) != 3 {
		t.Fatalf("engram command has %d elements, want 3", len(cmd))
	}
	// __replace__ must not appear in output
	if _, ok := eng["__replace__"]; ok {
		t.Fatal("__replace__ sentinel leaked into output")
	}
}

func TestMergeJSONObjectsReplaceSentinelNoBaseKey(t *testing.T) {
	base := []byte(`{}`)
	overlay := []byte(`{"a":{"__replace__":{"z":3}}}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal merged error = %v", err)
	}

	a, ok := got["a"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'a' to be a map; got: %T = %v", got["a"], got["a"])
	}
	if a["z"] != float64(3) {
		t.Fatalf("a.z = %v, want 3", a["z"])
	}
	if _, ok := a["__replace__"]; ok {
		t.Fatal("__replace__ sentinel leaked into output")
	}
}

func TestMergeJSONObjectsReplaceSentinelPreservesOtherKeys(t *testing.T) {
	base := []byte(`{"a":{"old":1},"b":"keep"}`)
	overlay := []byte(`{"a":{"__replace__":{"new":2}}}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal merged error = %v", err)
	}

	// "b" must survive untouched
	if got["b"] != "keep" {
		t.Fatalf("sibling key 'b' lost; got: %v", got)
	}

	a := got["a"].(map[string]any)
	// "old" must be gone (replaced atomically)
	if _, ok := a["old"]; ok {
		t.Fatalf("'a.old' survived __replace__; got: %v", a)
	}
	if a["new"] != float64(2) {
		t.Fatalf("a.new = %v, want 2", a["new"])
	}
}

func TestMergeJSONObjectsReplaceSentinelNotInOutput(t *testing.T) {
	base := []byte(`{"x":{"y":1}}`)
	overlay := []byte(`{"x":{"__replace__":{"z":2}}}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	if !json.Valid(merged) {
		t.Fatalf("merged output is not valid JSON: %s", string(merged))
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	// Walk all nested maps and ensure __replace__ never appears as a key
	var walk func(m map[string]any)
	walk = func(m map[string]any) {
		for k, v := range m {
			if k == "__replace__" {
				t.Fatalf("sentinel '__replace__' leaked into output: %v", m)
			}
			if sub, ok := v.(map[string]any); ok {
				walk(sub)
			}
		}
	}
	walk(got)
}

// ─── Issue #278: deep merge preserves stale wildcard permissions ──────────────

// TestMergeJSONObjects_Issue278_ReplaceSentinelFixesWildcard proves that
// wrapping the task block in __replace__ DOES remove the old wildcard,
// which is the proposed fix for issue #278.
func TestMergeJSONObjects_Issue278_ReplaceSentinelFixesWildcard(t *testing.T) {
	// Same base: existing user with old wildcard.
	base := []byte(`{
  "agent": {
    "sdd-orchestrator": {
      "permission": {
        "task": {
          "*": "deny",
          "sdd-*": "allow"
        }
      }
    }
  }
}`)

	// Overlay using __replace__ sentinel on the task block.
	overlay := []byte(`{
  "agent": {
    "sdd-orchestrator": {
      "permission": {
        "task": {
          "__replace__": {
            "*": "deny",
            "sdd-init": "allow",
            "sdd-explore": "allow",
            "sdd-propose": "allow",
            "sdd-spec": "allow",
            "sdd-design": "allow",
            "sdd-tasks": "allow",
            "sdd-apply": "allow",
            "sdd-verify": "allow",
            "sdd-archive": "allow",
            "sdd-onboard": "allow"
          }
        }
      }
    }
  }
}`)

	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("MergeJSONObjects() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatalf("Unmarshal merged error = %v", err)
	}

	agent := got["agent"].(map[string]any)
	orch := agent["sdd-orchestrator"].(map[string]any)
	perm := orch["permission"].(map[string]any)
	task := perm["task"].(map[string]any)

	// The wildcard MUST be gone.
	if _, hasWildcard := task["sdd-*"]; hasWildcard {
		t.Fatal("'sdd-*' survived __replace__ — sentinel is broken")
	}

	// __replace__ must NOT leak into output.
	if _, leaked := task["__replace__"]; leaked {
		t.Fatal("__replace__ sentinel leaked into output")
	}

	// All explicit entries must be present.
	expected := []string{"*", "sdd-init", "sdd-explore", "sdd-propose", "sdd-spec", "sdd-design", "sdd-tasks", "sdd-apply", "sdd-verify", "sdd-archive", "sdd-onboard"}
	for _, key := range expected {
		if _, ok := task[key]; !ok {
			t.Fatalf("expected key %q missing from task block after __replace__", key)
		}
	}

	if len(task) != len(expected) {
		t.Fatalf("task block has %d keys, want %d; got: %v", len(task), len(expected), task)
	}

	t.Logf("CONFIRMED: __replace__ produces exactly %d task keys (no wildcard)", len(task))
}

// exactNumberSettings holds number tokens that float64 decoding would round,
// reformat or reject (#1672); every rewrite must keep them verbatim.
const exactNumberSettings = `{"agent":{"custom":{"tools":{},"__managed_by":"brunnr-code/sdd"}},` +
	`"big":9007199254740993,"neg":-9007199254740995,"max":9223372036854775807,` +
	`"nested":{"list":[1.10,6.02214076e23,1E+2,{"min":-9223372036854775808}]},"huge":1e999}`

// assertExactNumbers re-decodes encoded independently of the code under test
// and checks every exactNumberSettings token at its own JSON path.
func assertExactNumbers(t *testing.T, encoded []byte) {
	t.Helper()
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(normalizeJSON(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		t.Fatalf("decode rewritten JSON error = %v:\n%s", err, encoded)
	}
	nested, _ := root["nested"].(map[string]any)
	list, _ := nested["list"].([]any)
	if len(list) != 4 {
		t.Fatalf("nested.list = %#v, want 4 entries:\n%s", nested["list"], encoded)
	}
	last, _ := list[3].(map[string]any)
	got := []any{root["big"], root["neg"], root["max"], list[0], list[1], list[2], last["min"], root["huge"]}
	want := []string{"9007199254740993", "-9007199254740995", "9223372036854775807",
		"1.10", "6.02214076e23", "1E+2", "-9223372036854775808", "1e999"}
	for i := range want {
		if number, ok := got[i].(json.Number); !ok || number.String() != want[i] {
			t.Fatalf("number %d = %#v, want exact %s:\n%s", i, got[i], want[i], encoded)
		}
	}
}

func TestUnmarshalJSONObjectKeepsStrictSingleDocument(t *testing.T) {
	for _, tc := range []struct{ name, raw, wantErr string }{
		{"second document", `{"a":1}{"b":2}`, "after top-level value"},
		{"trailing text", `{"a":1} trailing`, "after top-level value"},
		{"truncated", `{"a":`, "unexpected end of JSON input"},
		{"non-object", `[1]`, "cannot unmarshal array"},
		{"blank", "  \n", ""},
		{"jsonc", "// note\n{\"a\":1,}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalJSONObject([]byte(tc.raw))
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("UnmarshalJSONObject(%q) error = %v, want %q", tc.raw, err, tc.wantErr)
			}
		})
	}
}
