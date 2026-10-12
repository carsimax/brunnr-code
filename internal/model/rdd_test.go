package model

import "testing"

func TestSupportsReceiptDrivenDevelopmentIsTheClosedRDDRuntimeSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		agent AgentID
		want  bool
	}{
		{AgentClaudeCode, true},
		{AgentCodex, true},
		{AgentID("opencode"), false},
		{AgentID("unknown-runtime"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.agent), func(t *testing.T) {
			t.Parallel()
			if got := SupportsReceiptDrivenDevelopment(tt.agent); got != tt.want {
				t.Fatalf("SupportsReceiptDrivenDevelopment(%q) = %v, want %v", tt.agent, got, tt.want)
			}
		})
	}
}
