package model

import (
	"reflect"
	"testing"
)

func TestSupportedAgentIDsDropsRemovedAgentsInOrder(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: []string{}},
		{name: "only supported", in: []string{"codex", "claude-code"}, want: []string{"codex", "claude-code"}},
		{name: "stale entries dropped", in: []string{"opencode", "claude-code", "kiro-ide", "codex"}, want: []string{"claude-code", "codex"}},
		{name: "only stale", in: []string{"opencode", "pi"}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SupportedAgentIDs(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SupportedAgentIDs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestKnownComponentIDsDropsUnknownInOrder(t *testing.T) {
	in := []ComponentID{"retired-component", ComponentSkills, "", ComponentEngram}
	want := []ComponentID{ComponentSkills, ComponentEngram}
	if got := KnownComponentIDs(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("KnownComponentIDs(%v) = %v, want %v", in, got, want)
	}
}
