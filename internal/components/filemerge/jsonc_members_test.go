package filemerge

import (
	"slices"
	"testing"
)

func TestRemoveJSONCMembersNestedPathAndCompactJSON(t *testing.T) {
	raw := `{"agent":{"example-orchestrator":{"permission":{"task":{"*":"deny","example-apply":"allow","jd-judge-a":"allow","example-verify":"allow"}}}}}`
	got, kept, err := RemoveJSONCMembers([]byte(raw), []string{"agent", "example-orchestrator", "permission", "task"}, []string{"example-apply", "example-verify"}, false)
	if err != nil || len(kept) != 0 {
		t.Fatalf("kept = %v, err = %v", kept, err)
	}
	if want := `{"agent":{"example-orchestrator":{"permission":{"task":{"*":"deny","jd-judge-a":"allow"}}}}}`; string(got) != want {
		t.Fatalf("nested removal = %s", got)
	}
	got, _, err = RemoveJSONCMembers([]byte(`{"agents":{"only":{}}, "x": 1}`), []string{"agents"}, []string{"only"}, true)
	if err != nil || string(got) != `{"x": 1}` {
		t.Fatalf("drop first member = %s, %v", got, err)
	}
	got, _, err = RemoveJSONCMembers([]byte(`{"x": 1, "agents":{"only":{}}}`), []string{"agents"}, []string{"only"}, true)
	if err != nil || string(got) != `{"x": 1}` {
		t.Fatalf("drop last member = %s, %v", got, err)
	}
}

// A comment on its own line after the last member stays, and the preceding
// comma stays too, so the comment never ends up nested in another value.
func TestRemoveJSONCMembersLastMemberBeforeClosingComment(t *testing.T) {
	raw := "{\n  \"agent\": {\"mine\": {}},\n  \"agents\": {\n    \"example-apply\": {}\n  }\n  // closing note\n}\n"
	got, kept, err := RemoveJSONCMembers([]byte(raw), []string{"agents"}, []string{"example-apply"}, true)
	if err != nil || len(kept) != 0 {
		t.Fatalf("kept = %v, err = %v", kept, err)
	}
	if want := "{\n  \"agent\": {\"mine\": {}},\n  // closing note\n}\n"; string(got) != want {
		t.Fatalf("removal = %q, want %q", got, want)
	}
	if JSONCTopLevelValueHasComments(got, "agent") {
		t.Fatal("the closing comment became nested in agent")
	}
	if _, err := UnmarshalJSONObject(got); err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
}

func TestRemoveJSONCMembersKeepsCommentedMembersAndRefusesDuplicates(t *testing.T) {
	raw := `{
  "agent": {
    // my notes about this agent
    "example-apply": {"mode": "subagent"},
    "example-spec": {"mode": "subagent" /* tuned */},
    "example-tasks": {"mode": "subagent"}, // why I keep it
    "example-\u0076erify": {"mode": "subagent"},
    "example-design": {"mode": "subagent"}
  }
}
`
	got, kept, err := RemoveJSONCMembers([]byte(raw), []string{"agent"}, []string{"example-apply", "example-spec", "example-tasks", "example-verify", "example-design"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"example-apply", "example-spec", "example-tasks", "example-verify"}; !slices.Equal(kept, want) {
		t.Fatalf("kept = %v, want %v", kept, want)
	}
	root, err := UnmarshalJSONObject(got)
	if err != nil {
		t.Fatal(err)
	}
	agent := root["agent"].(map[string]any)
	if _, ok := agent["example-design"]; ok || len(agent) != 4 {
		t.Fatalf("remaining agents = %v", agent)
	}

	duplicate := []byte(`{"agent":{"example-apply":{}},"agent":{}}`)
	if out, _, err := RemoveJSONCMembers(duplicate, []string{"agent"}, []string{"example-apply"}, false); err == nil || string(out) != string(duplicate) {
		t.Fatalf("duplicate keys accepted: %s, %v", out, err)
	}
}

// A line comment directly above a member is attached to it even when the
// comment text ends in a separator a backward scan would take for JSON.
func TestRemoveJSONCMembersKeepsMemberUnderCommentEndingInSeparator(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"comma, middle member", "{\n  \"agent\": {\n    \"mine\": {},\n    // tuned: model, variant,\n    \"example-apply\": {\"mode\": \"subagent\"},\n    \"other\": {}\n  }\n}\n"},
		{"brace, middle member", "{\n  \"agent\": {\n    \"mine\": {},\n    // see {\n    \"example-apply\": {\"mode\": \"subagent\"},\n    \"other\": {}\n  }\n}\n"},
		{"comma, last member", "{\n  \"agent\": {\n    \"mine\": {},\n    // keep this one,\n    \"example-apply\": {\"mode\": \"subagent\"}\n  }\n}\n"},
		{"brace, last member", "{\n  \"agent\": {\n    \"mine\": {},\n    // old config {\n    \"example-apply\": {\"mode\": \"subagent\"}\n  }\n}\n"},
		{"block comment on the same line", "{\n  \"agent\": {\"mine\": {}, /* why, */ \"example-apply\": {}}\n}\n"},
		{"first member", "{\n  \"agent\": {\n    // note {\n    \"example-apply\": {},\n    \"mine\": {}\n  }\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, kept, err := RemoveJSONCMembers([]byte(tc.raw), []string{"agent"}, []string{"example-apply"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(kept, []string{"example-apply"}) || string(got) != tc.raw {
				t.Fatalf("kept = %v, document:\n%s", kept, got)
			}
		})
	}
}

// Removing a last member after commented members leaves valid JSONC with
// every comment and its own separators intact.
func TestRemoveJSONCMembersLastMemberAfterCommentedMembersStaysValid(t *testing.T) {
	raw := "{\n  \"agent\": {\n    \"a\": 1, // first, with a comma,\n    // about b {\n    \"b\": 2,\n    \"example-apply\": {}\n  }\n}\n"
	got, kept, err := RemoveJSONCMembers([]byte(raw), []string{"agent"}, []string{"example-apply"}, false)
	if err != nil || len(kept) != 0 {
		t.Fatalf("kept = %v, err = %v", kept, err)
	}
	want := "{\n  \"agent\": {\n    \"a\": 1, // first, with a comma,\n    // about b {\n    \"b\": 2\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("removal = %q, want %q", got, want)
	}
	root, err := UnmarshalJSONObject(got)
	if err != nil {
		t.Fatalf("result does not parse: %v", err)
	}
	if agent := root["agent"].(map[string]any); len(agent) != 2 {
		t.Fatalf("remaining = %v", agent)
	}
}

// An empty path addresses the document root, so top-level members such as a
// managed settings key are removed without touching the user's comments.
func TestRemoveJSONCMembersTopLevel(t *testing.T) {
	raw := "{\n  // note\n  \"model\": \"opus\",\n  \"outputStyle\": \"X\",\n}\n"
	got, kept, err := RemoveJSONCMembers([]byte(raw), nil, []string{"outputStyle"}, true)
	if err != nil || len(kept) != 0 {
		t.Fatalf("kept = %v, err = %v", kept, err)
	}
	if want := "{\n  // note\n  \"model\": \"opus\",\n}\n"; string(got) != want {
		t.Fatalf("top-level removal = %q, want %q", got, want)
	}
}
