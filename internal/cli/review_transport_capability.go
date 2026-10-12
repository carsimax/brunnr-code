package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/v4/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/opencode"
)

// openCodeRelayContractEnvironment carries the managed V2 plugin's relay
// declaration into its relay child and ordinary host shells.
const openCodeRelayContractEnvironment = "GENTLE_AI_OPENCODE_RELAY_CONTRACT"

// openCodeRelayContractV2 is the exact relay contract the managed OpenCode V2
// review plugin declares. Together with a detected V2 runtime it admits the
// same Go-owned provider-injected transport V1 uses (T4c: proven on a real
// OpenCode 2.0.19 host with the gate un-stubbed).
const openCodeRelayContractV2 = "gentle-ai.opencode-relay/v2-staged"

// openCodeRelayDeclaresV2 reports whether this process was started under the
// exact managed V2 relay declaration.
func openCodeRelayDeclaresV2() bool {
	return os.Getenv(openCodeRelayContractEnvironment) == openCodeRelayContractV2
}

const reviewImmutableTransportUnsupportedCode = "immutable_review_transport_unsupported"

var reviewImmutableTransportUnsupportedReason = reviewPreflightReason{
	Code:       reviewImmutableTransportUnsupportedCode,
	Message:    "The active runtime cannot provide immutable receipt-review transport.",
	NextAction: "stop",
}

type reviewImmutableTransport string

const (
	reviewImmutableTransportUnsupported         reviewImmutableTransport = "unsupported"
	reviewImmutableTransportClaudePromptCarried reviewImmutableTransport = "claude_prompt_carried"
	// reviewImmutableTransportOpenCodeProviderInjected is a one-Task,
	// one-process relay. Go owns the provider contract, prompt materialization,
	// admission, capture, and completion binding; the OpenCode plugin only
	// relays opaque frames through its live child process.
	reviewImmutableTransportOpenCodeProviderInjected reviewImmutableTransport = "opencode_provider_injected"
	// reviewImmutableTransportCodexAdvisoryScratchProcess retains the canonical
	// Go-owned provider contract across a fresh Codex subprocess boundary.
	reviewImmutableTransportCodexAdvisoryScratchProcess reviewImmutableTransport = "codex_advisory_scratch_process"
)

type reviewImmutableRuntimePolicy struct {
	Eligible  bool
	Transport reviewImmutableTransport
	// Refusal, when set, is the complete operator-facing reason a host
	// condition refused an otherwise supported runtime.
	Refusal error
}

// reviewOpenCodeRuntimeProbe memoizes the OpenCode runtime answer, failures
// included, for the life of the process, keyed by the executable PATH
// resolves. Every gate in one process (assess, STATUS, START, consent, relay,
// capture) therefore reads the same answer and eligibility cannot flap
// between them (#4984, #5058, #5117).
var reviewOpenCodeRuntimeProbe struct {
	sync.Mutex
	answers map[string]reviewOpenCodeRuntimeAnswer
}

type reviewOpenCodeRuntimeAnswer struct {
	major opencode.RuntimeMajor
	err   error
}

func reviewOpenCodeRuntimeMajor() (opencode.RuntimeMajor, error) {
	executable, err := exec.LookPath("opencode")
	if err != nil {
		executable = ""
	}
	reviewOpenCodeRuntimeProbe.Lock()
	defer reviewOpenCodeRuntimeProbe.Unlock()
	if answer, ok := reviewOpenCodeRuntimeProbe.answers[executable]; ok {
		return answer.major, answer.err
	}
	major, err := opencode.DetectRuntimeMajor(context.Background())
	if reviewOpenCodeRuntimeProbe.answers == nil {
		reviewOpenCodeRuntimeProbe.answers = make(map[string]reviewOpenCodeRuntimeAnswer)
	}
	reviewOpenCodeRuntimeProbe.answers[executable] = reviewOpenCodeRuntimeAnswer{major: major, err: err}
	return major, err
}

// reviewOpenCodeRuntimeRefusal checks the OpenCode host conditions. The relay
// declaration and the detected runtime must agree: the V1 plugin declares
// nothing and runs on V1; the managed V2 plugin declares exactly the V2 relay
// contract and runs on V2. Any other declaration refuses before the PATH
// probe, and a disagreeing pair refuses too, so a V2 host whose PATH resolves
// a coexisting V1 binary (or the reverse) never inherits the other runtime's
// capability. Neither the declaration nor version evidence alone can enable
// the transport.
func reviewOpenCodeRuntimeRefusal() error {
	declaration := os.Getenv(openCodeRelayContractEnvironment)
	if declaration != "" && declaration != openCodeRelayContractV2 {
		return reviewOpenCodeRuntimeUnproven()
	}
	major, err := reviewOpenCodeRuntimeMajor()
	if errors.Is(err, opencode.ErrRuntimeVersionTimeout) {
		// refusal:by-design world-action: an unproven runtime cannot receive immutable review authority; the exit is re-running once the host answers the probe
		return errors.New("OpenCode immutable receipt-review eligibility could not be proven because `opencode --version` timed out in this process; nothing was started, so re-run the same command once `opencode --version` answers promptly")
	}
	want := opencode.RuntimeV1
	if declaration == openCodeRelayContractV2 {
		want = opencode.RuntimeV2
	}
	if err != nil || major != want {
		return reviewOpenCodeRuntimeUnproven()
	}
	return nil
}

// reviewOpenCodeRuntimeUnproven names the host condition OpenCode failed, so a
// supported-runtime list that includes OpenCode does not contradict it.
func reviewOpenCodeRuntimeUnproven() error {
	return fmt.Errorf("the active runtime is not eligible for immutable receipt review: OpenCode qualifies only when `opencode --version` reports a stable 1.x runtime with no relay declaration, or a stable 2.x runtime under the managed V2 plugin's relay declaration%s", reviewTransportRefusalExitGuidance())
}

// reviewImmutableRuntimeCapability is the compiled receipt-review boundary.
// Generic adapter features and caller-supplied claims cannot expand it.
func reviewImmutableRuntimeCapability(agent model.AgentID) reviewImmutableRuntimePolicy {
	return reviewImmutableRuntimeCapabilityChecking(agent, reviewOpenCodeRuntimeRefusal)
}

func reviewImmutableRuntimeCapabilityChecking(agent model.AgentID, openCodeRuntimeRefusal func() error) reviewImmutableRuntimePolicy {
	policy := reviewImmutableRuntimePolicy{Transport: reviewImmutableTransportUnsupported}
	switch agent {
	case model.AgentClaudeCode:
		policy.Eligible = true
	case model.AgentCodex:
		policy.Eligible = true
	case model.AgentOpenCode:
		if refusal := openCodeRuntimeRefusal(); refusal != nil {
			policy.Refusal = refusal
			return policy
		}
		policy.Eligible = true
	default:
		return policy
	}
	manifest, err := capabilitymanifest.ForAgent(agent)
	if err != nil || !manifest.Advertises(capabilitymanifest.ContractImmutableReviewExecutorV1) {
		return policy
	}
	switch agent {
	case model.AgentClaudeCode:
		policy.Transport = reviewImmutableTransportClaudePromptCarried
	case model.AgentOpenCode:
		policy.Transport = reviewImmutableTransportOpenCodeProviderInjected
	case model.AgentCodex:
		policy.Transport = reviewImmutableTransportCodexAdvisoryScratchProcess
	}
	return policy
}

func (capability reviewImmutableRuntimePolicy) supportsImmutableReceiptReview() bool {
	return capability.Transport == reviewImmutableTransportClaudePromptCarried ||
		capability.Transport == reviewImmutableTransportOpenCodeProviderInjected ||
		capability.Transport == reviewImmutableTransportCodexAdvisoryScratchProcess
}

// reviewTransportSupportedRuntimeIDs derives the actionable runtime list from
// the compiled boundary. A refused runtime cannot appear as a substitute.
// OpenCode's host conditions are never probed here: the list names what this
// binary supports, and an OpenCode refusal names its own host condition.
func reviewTransportSupportedRuntimeIDs() []string {
	supported := make([]string, 0)
	for _, agent := range catalog.AllAgents() {
		if reviewImmutableRuntimeCapabilityChecking(agent.ID, func() error { return nil }).supportsImmutableReceiptReview() {
			supported = append(supported, string(agent.ID))
		}
	}
	return supported
}

func reviewTransportRefusalExitGuidance() string {
	return "; exit receipt-driven review with `gentle-ai review mode disable --scope clone --cwd <repo>`; supported immutable review runtimes: " +
		strings.Join(reviewTransportSupportedRuntimeIDs(), ", ")
}

// reviewRuntimeWithImmutableTransport accepts only the exact compiled runtime
// identities. It never selects a substitute transport for an unsupported one.
func reviewRuntimeWithImmutableTransport(agent string) (model.AgentID, error) {
	if strings.TrimSpace(agent) == "" || strings.TrimSpace(agent) != agent {
		// refusal:by-design world-action: an unknown runtime cannot safely receive immutable review authority
		return "", errors.New("the active review runtime is unknown")
	}
	identity := model.AgentID(agent)
	capability := reviewImmutableRuntimeCapability(identity)
	if capability.Refusal != nil {
		return "", capability.Refusal
	}
	if !capability.Eligible {
		// refusal:by-design world-action: runtimes outside the fixed RDD policy cannot receive immutable review authority
		return "", fmt.Errorf("the active runtime is not eligible for immutable receipt review%s", reviewTransportRefusalExitGuidance())
	}
	if !capability.supportsImmutableReceiptReview() {
		// refusal:by-design world-action: unsupported transport cannot bind immutable evidence or capture an admissible result
		return "", fmt.Errorf("the active runtime lacks immutable receipt-review transport%s", reviewTransportRefusalExitGuidance())
	}
	return identity, nil
}

func reviewRuntimeAgentCount(args []string) int {
	count := 0
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		nameValue := strings.TrimPrefix(strings.TrimPrefix(argument, "-"), "-")
		if nameValue == argument || nameValue == "" {
			continue
		}
		name, hasValue := nameValue, false
		if separator := strings.IndexByte(nameValue, '='); separator >= 0 {
			name, hasValue = nameValue[:separator], true
		}
		if name != "agent" {
			continue
		}
		count++
		if !hasValue {
			index++
		}
	}
	return count
}
