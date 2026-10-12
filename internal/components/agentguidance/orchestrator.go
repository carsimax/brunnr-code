package agentguidance

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/gentleman-programming/gentle-ai/v4/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/v4/internal/assets"
	"github.com/gentleman-programming/gentle-ai/v4/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// OrchestratorSectionID is the managed marker section that owns the
// orchestrator instructions (coordinator role, lossless blocking prompts,
// delegation and verification gates, and the native review lifecycle).
//
// The ODD protocol itself is owned by the routing section: every orchestrator
// asset points at `## Implementation Routing` instead of restating it, so the
// two sections together carry the protocol exactly once.
const OrchestratorSectionID = "orchestrator"

// legacyOrchestratorSectionID is the marker v3.7.0 installed the same
// orchestrator under, when the retired SDD component owned it. Upgrades convert
// that block in place so a prompt never carries two orchestrators.
const legacyOrchestratorSectionID = "sdd-orchestrator"

const (
	sharedOrchestratorSectionsAsset = "skills/_shared/odd-orchestrator-sections.md"
	runtimeAgentIDPlaceholder       = "{{GENTLE_AI_RUNTIME_AGENT_ID}}"
	reviewExecutionHeading          = "#### Review Execution Contract"
	reviewExecutionNextHeading      = "Cost and Context Balance"
	providerDefectHandoffHeading    = "#### Gentle AI Provider Defect Handoff (MANDATORY)"
	nativeCheckingHeading           = "#### Native Checking Contract"
	nativeCheckingSection           = "Native Checking Contract"
	// oddOnlySectionSuffix names the shared-section variant a runtime without
	// receipt-driven development receives in place of the canonical body.
	oddOnlySectionSuffix = " (ODD only)"
)

// nonRDDReplacements rewrites the inline review wording the runtime assets
// share into its ODD-only form. Whole sections are handled separately.
var nonRDDReplacements = strings.NewReplacer(
	"| Tests, builds, installs, or native review actions |", "| Tests, builds, installs, or verification actions |",
	"| High-risk change, or long suites, builds, installs, or native review actions of a large task |", "| High-risk change, or long suites, builds, installs, or verification actions of a large task |",
	"tests, builds, installs, and native review actors may use fresh workers", "tests, builds, installs, and verification actors may use fresh workers",
	"run applicable tests, builds, and native review actions as bounded steps", "run applicable tests, builds, and verification actions as bounded steps",
	"- Let the native review and delivery providers select checking and delivery actions; repeated gates reuse exact authority and never reopen review for unchanged content.", "- Let the user and ordinary repository policy decide delivery; do not infer authorization from checking output.",
	"- Let native review select its bounded checking plan; delivery remains human-owned under ordinary repository policy.", "- Let the user and ordinary repository policy decide delivery; do not infer authorization from checking output.",
)

// nonRDDLeakMarkers fail a render closed: a runtime without receipt-driven
// development must never be handed a piece of the review lifecycle because an
// asset gained wording the gate above does not know about yet.
var nonRDDLeakMarkers = []string{
	"RDD",
	"receipt",
	"Receipt",
	"refuter",
	"native review",
	"Native review",
	"gentle-ai review",
	"review-integration",
	"Native Compact Review Orchestration",
	"Provider Defect Handoff",
}

// ErrMissingReviewContract fails closed when a runtime that advertises the
// native review transport would receive an orchestrator without its review
// execution contract: installing a review lifecycle with no procedure in it is
// worse than refusing the install.
var ErrMissingReviewContract = errors.New("review execution contract source is not configured")

// ReviewContractSource renders the runtime-bound native review execution
// contract. It is reviewassets.ReviewExecutionContractFor in production; the
// seam exists only because reviewassets already imports this package. Callers
// pass it through RoutingOptions.ReviewContract or RenderOrchestratorWithSource.
type ReviewContractSource func(model.AgentID) (string, error)

var (
	reviewContractMu     sync.RWMutex
	reviewContractSource ReviewContractSource
)

// reviewExecutionContract returns the contract for a runtime that receives
// receipt-driven development, or ok=false for one that does not. A non-nil
// source wins over the package-level fallback.
func reviewExecutionContract(agent model.AgentID, source ReviewContractSource) (contract string, ok bool, err error) {
	if _, err := capabilitymanifest.ForAgent(agent); err != nil {
		return "", false, err
	}
	if !model.SupportsReceiptDrivenDevelopment(agent) {
		return "", false, nil
	}
	if source == nil {
		reviewContractMu.RLock()
		source = reviewContractSource
		reviewContractMu.RUnlock()
	}
	if source == nil {
		return "", false, fmt.Errorf("review contract source was not wired for %q (set RoutingOptions.ReviewContract): %w", agent, ErrMissingReviewContract)
	}
	contract, err = source(agent)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(contract) == "" {
		return "", false, ErrMissingReviewContract
	}
	return contract, true, nil
}

// sharedOrchestratorSectionPlaceholder matches one {{GENTLE_AI_ODD_SECTION:<name>}}.
var sharedOrchestratorSectionPlaceholder = regexp.MustCompile(`\{\{GENTLE_AI_ODD_SECTION:([^}]*)\}\}`)

// orchestratorAsset selects the embedded orchestrator for one supported
// runtime, or "" when the runtime has none.
func orchestratorAsset(agent model.AgentID) string {
	switch agent {
	case model.AgentClaudeCode:
		return "claude/orchestrator.md"
	case model.AgentCodex:
		return "codex/orchestrator.md"
	default:
		return ""
	}
}

// RenderOrchestratorWithSource is RenderOrchestrator with an explicit review
// contract source, which takes precedence over the package-level fallback.
func RenderOrchestratorWithSource(agent model.AgentID, source ReviewContractSource) (string, error) {
	return renderOrchestrator(agent, source, false)
}

// renderOrchestrator is the single render path. annotate keeps the shared
// module fragment markers for the internal core/module construction; the
// public render always strips those exact marker lines.
func renderOrchestrator(agent model.AgentID, source ReviewContractSource, annotate bool) (string, error) {
	path := orchestratorAsset(agent)
	if path == "" {
		return "", fmt.Errorf("render orchestrator for %q: unsupported agent", agent)
	}
	content, err := assets.Read(path)
	if err != nil {
		return "", fmt.Errorf("render orchestrator for %q: %w", agent, err)
	}

	rdd := model.SupportsReceiptDrivenDevelopment(agent)
	content, err = expandSharedOrchestratorSections(content, rdd, annotate)
	if err != nil {
		return "", fmt.Errorf("render orchestrator for %q: %w", agent, err)
	}

	// v3.7.0 removed the section for runtimes that do not advertise the
	// review transport instead of handing them a lifecycle they cannot run.
	contract, reviews, err := reviewExecutionContract(agent, source)
	if err != nil {
		return "", fmt.Errorf("render orchestrator for %q: %w", agent, err)
	}
	if reviews {
		content = replaceSectionBody(content, reviewExecutionHeading, reviewExecutionNextHeading, contract)
	} else {
		content = removeSection(content, reviewExecutionHeading, reviewExecutionNextHeading)
	}

	if !rdd {
		content, err = stripReceiptDrivenDevelopment(content)
		if err != nil {
			return "", fmt.Errorf("render orchestrator for %q: %w", agent, err)
		}
	}

	content = strings.ReplaceAll(content, runtimeAgentIDPlaceholder, string(agent))
	if strings.Contains(content, "{{GENTLE_AI_") {
		return "", fmt.Errorf("render orchestrator for %q: unresolved template placeholder in %s", agent, path)
	}
	if !annotate && strings.Contains(content, orchestratorFragmentMarkerToken) {
		return "", fmt.Errorf("render orchestrator for %q: malformed module fragment marker survived the render", agent)
	}
	return strings.TrimSpace(content) + "\n", nil
}

// stripReceiptDrivenDevelopment turns a rendered orchestrator into its ODD-only
// form for a runtime without receipt-driven development.
func stripReceiptDrivenDevelopment(content string) (string, error) {
	content = removeHeadingBlock(content, providerDefectHandoffHeading)

	shared, err := assets.Read(sharedOrchestratorSectionsAsset)
	if err != nil {
		return "", err
	}
	checking := sharedOrchestratorSection(shared, nativeCheckingSection+oddOnlySectionSuffix)
	if checking == "" {
		return "", fmt.Errorf("shared orchestrator section %q has no canonical body", nativeCheckingSection+oddOnlySectionSuffix)
	}
	content = replaceHeadingBlockBody(content, nativeCheckingHeading, checking)
	content = nonRDDReplacements.Replace(content)

	for _, marker := range nonRDDLeakMarkers {
		if strings.Contains(content, marker) {
			return "", fmt.Errorf("orchestrator without receipt-driven development still carries review content %q", marker)
		}
	}
	return content, nil
}

// expandSharedOrchestratorSections resolves every shared-section placeholder in
// one pass. A canonical body is literal text, so a placeholder inside one is not
// a nested reference. An unknown section fails rather than shipping a prompt
// with a literal template token in it. Without receipt-driven development, a
// section's "(ODD only)" variant replaces its canonical body when one exists.
// annotate keeps the module fragment markers inside the canonical bodies.
func expandSharedOrchestratorSections(content string, rdd, annotate bool) (string, error) {
	shared, err := assets.Read(sharedOrchestratorSectionsAsset)
	if err != nil {
		return "", err
	}
	var missing []string
	expanded := sharedOrchestratorSectionPlaceholder.ReplaceAllStringFunc(content, func(match string) string {
		name := sharedOrchestratorSectionPlaceholder.FindStringSubmatch(match)[1]
		section := sharedOrchestratorSection
		if annotate {
			section = annotatedSharedOrchestratorSection
		}
		body := ""
		if !rdd {
			body = section(shared, name+oddOnlySectionSuffix)
		}
		if body == "" {
			body = section(shared, name)
		}
		if body == "" {
			missing = append(missing, name)
			return match
		}
		return body
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("shared orchestrator sections %q have no canonical body", missing)
	}
	return expanded, nil
}

// sharedOrchestratorSection returns a canonical body without its module
// fragment marker lines, byte for byte the body before the markers existed.
func sharedOrchestratorSection(shared, name string) string {
	return strings.TrimSpace(stripOrchestratorFragmentMarkers(rawSharedOrchestratorSection(shared, name)))
}

func annotatedSharedOrchestratorSection(shared, name string) string {
	return strings.TrimSpace(rawSharedOrchestratorSection(shared, name))
}

func rawSharedOrchestratorSection(shared, name string) string {
	open := "<!-- odd-orchestrator-section:" + name + ":start -->"
	closing := "<!-- odd-orchestrator-section:" + name + ":end -->"
	start := strings.Index(shared, open)
	end := strings.Index(shared, closing)
	if start < 0 || end < start {
		return ""
	}
	return shared[start+len(open) : end]
}

// sectionBounds returns the byte range of the section that opens at heading and
// ends before the next heading named nextHeading, at any level.
func sectionBounds(content, heading, nextHeading string) (int, int, bool) {
	start := strings.Index(content, heading)
	if start < 0 {
		return 0, 0, false
	}
	end := len(content)
	remainder := content[start+len(heading):]
	for _, candidate := range []string{"\n#### " + nextHeading, "\n### " + nextHeading, "\n## " + nextHeading} {
		if relative := strings.Index(remainder, candidate); relative >= 0 {
			end = start + len(heading) + relative + 1
			break
		}
	}
	return start, end, true
}

// headingBlockBounds returns the byte range of the block that opens at the
// heading line and ends before the next heading of the same or a higher level.
func headingBlockBounds(content, heading string) (int, int, bool) {
	start := lineStartIndex(content, heading+"\n")
	if start < 0 {
		return 0, 0, false
	}
	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	offset := start + len(heading) + 1
	for offset < len(content) {
		lineEnd := strings.IndexByte(content[offset:], '\n')
		line := content[offset:]
		if lineEnd >= 0 {
			line = content[offset : offset+lineEnd]
		}
		if hashes := len(line) - len(strings.TrimLeft(line, "#")); hashes > 0 && hashes <= level && strings.HasPrefix(line[hashes:], " ") {
			return start, offset, true
		}
		if lineEnd < 0 {
			break
		}
		offset += lineEnd + 1
	}
	return start, len(content), true
}

// removeHeadingBlock drops the heading and its body.
func removeHeadingBlock(content, heading string) string {
	start, end, ok := headingBlockBounds(content, heading)
	if !ok {
		return content
	}
	return strings.TrimRight(content[:start], "\n") + "\n\n" + strings.TrimLeft(content[end:], "\n")
}

// replaceHeadingBlockBody keeps the heading and replaces its body.
func replaceHeadingBlockBody(content, heading, body string) string {
	start, end, ok := headingBlockBounds(content, heading)
	if !ok {
		return content
	}
	return content[:start] + heading + "\n\n" + strings.TrimSpace(body) + "\n\n" + strings.TrimLeft(content[end:], "\n")
}

func replaceSectionBody(content, heading, nextHeading, body string) string {
	start, end, ok := sectionBounds(content, heading, nextHeading)
	if !ok {
		return content
	}
	replacement := heading + "\n\n" + strings.TrimSpace(body) + "\n\n"
	return strings.TrimRight(content[:start], "\n") + "\n\n" + replacement + strings.TrimLeft(content[end:], "\n")
}

func removeSection(content, heading, nextHeading string) string {
	start, end, ok := sectionBounds(content, heading, nextHeading)
	if !ok {
		return content
	}
	return strings.TrimRight(content[:start], "\n") + "\n\n" + strings.TrimLeft(content[end:], "\n")
}

// injectOrchestratorSection merges the orchestrator block into existing
// guidance, keeping the v3.7.0 layout: the orchestrator sits ahead of the
// routing block it points to, and a legacy SDD-owned block is converted where
// it stands. Content outside managed markers is never touched.
func injectOrchestratorSection(existing, orchestrator string) string {
	existing = migrateLegacyOrchestratorSection(existing)

	open := managedOpenMarker(OrchestratorSectionID)
	if strings.Contains(existing, open) {
		return filemerge.InjectMarkdownSection(existing, OrchestratorSectionID, orchestrator)
	}

	routing := lineStartIndex(existing, managedOpenMarker(RoutingSectionID))
	if routing < 0 {
		return filemerge.InjectMarkdownSection(existing, OrchestratorSectionID, orchestrator)
	}
	block := open + "\n" + strings.TrimRight(orchestrator, "\n") + "\n" + managedCloseMarker(OrchestratorSectionID) + "\n\n"
	return existing[:routing] + block + existing[routing:]
}

// migrateLegacyOrchestratorSection renames the first well-formed v3.7.0
// sdd-orchestrator block to the current marker so the fresh orchestrator is
// injected in its place, then drops every remaining legacy copy.
func migrateLegacyOrchestratorSection(existing string) string {
	legacyOpen := managedOpenMarker(legacyOrchestratorSectionID)
	legacyClose := managedCloseMarker(legacyOrchestratorSectionID)
	if !strings.Contains(existing, legacyOpen) && !strings.Contains(existing, legacyClose) {
		return existing
	}

	if !strings.Contains(existing, managedOpenMarker(OrchestratorSectionID)) {
		openIdx := strings.Index(existing, legacyOpen)
		closeIdx := strings.Index(existing, legacyClose)
		if openIdx >= 0 && closeIdx > openIdx {
			existing = existing[:openIdx] + managedOpenMarker(OrchestratorSectionID) +
				existing[openIdx+len(legacyOpen):closeIdx] + managedCloseMarker(OrchestratorSectionID) +
				existing[closeIdx+len(legacyClose):]
		}
	}
	return filemerge.InjectMarkdownSection(existing, legacyOrchestratorSectionID, "")
}

func managedOpenMarker(sectionID string) string  { return "<!-- gentle-ai:" + sectionID + " -->" }
func managedCloseMarker(sectionID string) string { return "<!-- /gentle-ai:" + sectionID + " -->" }

// lineStartIndex returns the first index of needle that starts a line.
func lineStartIndex(content, needle string) int {
	offset := 0
	for {
		idx := strings.Index(content[offset:], needle)
		if idx < 0 {
			return -1
		}
		abs := offset + idx
		if abs == 0 || content[abs-1] == '\n' {
			return abs
		}
		offset = abs + 1
	}
}
