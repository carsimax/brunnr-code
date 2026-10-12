package model

// receiptDrivenDevelopmentAgents is the closed set of runtimes that receive
// receipt-driven development (RDD): its orchestrator and routing instructions
// and its native review agents (review-* lenses, refuter, and validator). Every
// other runtime receives Organic Driven Development only.
//
// It is the single source of truth for that gate and must equal the runtimes
// whose capability manifest advertises the review transport; a parity test in
// agentguidance fails when the two drift.
var receiptDrivenDevelopmentAgents = []AgentID{
	AgentClaudeCode,
	AgentCodex,
}

// SupportsReceiptDrivenDevelopment reports whether agent receives
// receipt-driven development content and agents. Unknown agents do not.
func SupportsReceiptDrivenDevelopment(agent AgentID) bool {
	for _, candidate := range receiptDrivenDevelopmentAgents {
		if candidate == agent {
			return true
		}
	}
	return false
}
