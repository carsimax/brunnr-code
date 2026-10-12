package assets

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestODDOnlyEmbeddedInventory guards every shipped runtime, not just one
// install layout. Mixed orchestrators are retained and rewritten separately.
func TestODDOnlyEmbeddedInventory(t *testing.T) {
	var forbidden []string
	orchestrators := map[string]bool{}
	err := fs.WalkDir(FS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		parts := strings.Split(path, "/")
		name := entry.Name()
		for _, part := range parts {
			if strings.HasPrefix(part, "sdd-") || strings.HasPrefix(part, "gentle-sdd-") || part == "sdd" || part == "openspec-convention.md" {
				// Existing orchestrators are mixed ODD/RDD assets; their
				// SDD content must be rewritten, not removed wholesale.
				if name != "sdd-orchestrator.md" {
					forbidden = append(forbidden, path)
				}
				break
			}
		}
		if name == "orchestrator.md" || name == "sdd-orchestrator.md" {
			orchestrators[parts[0]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(orchestrators) == 0 {
		t.Fatal("embedded inventory has no retained orchestrator assets")
	}
	for _, runtime := range []string{"claude", "codex", "generic"} {
		if !orchestrators[runtime] {
			t.Errorf("%s lost its ODD/RDD orchestrator asset", runtime)
		}
	}
	if len(forbidden) != 0 {
		t.Errorf("embedded inventory still ships SDD-only assets: %s", strings.Join(forbidden, ", "))
	}
}

// TestSharedReferencesDoNotAdvertiseSDD checks the content installed under
// skills/_shared, not only the skill names in the embedded inventory.
func TestSharedReferencesDoNotAdvertiseSDD(t *testing.T) {
	entries, err := fs.ReadDir(FS, "skills/_shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no shared references embedded")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := "skills/_shared/" + entry.Name()
		t.Run(entry.Name(), func(t *testing.T) {
			for lineNumber, line := range strings.Split(MustRead(path), "\n") {
				if sharedReferenceInvitesSDD(line) {
					t.Errorf("%s:%d advertises retired workflow: %s", path, lineNumber+1, line)
				}
			}
		})
	}
}

func sharedReferenceInvitesSDD(line string) bool {
	lower := strings.ToLower(line)
	// Historical negative controls are evidence, not instructions.
	if strings.Contains(lower, "historical negative control:") {
		return false
	}
	return regexp.MustCompile(`\bsdd\b|sdd[-/]|openspec|spec-driven`).MatchString(lower)
}

func TestSharedReferenceNegativeControls(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"Historical negative control: do not run sdd-apply or create OpenSpec files.", false},
		{"<!-- odd-orchestrator-section:Language Domain Contract:start -->", false},
		{"<!-- sdd-orchestrator-section:Language Domain Contract:start -->", true},
		{"Run sdd-apply and persist in openspec/.", true},
		{"Use spec-driven development for this task.", true},
	} {
		if got := sharedReferenceInvitesSDD(tc.line); got != tc.want {
			t.Errorf("sharedReferenceInvitesSDD(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestClaudeODDOnlyOrchestrator(t *testing.T) {
	content := MustRead("claude/orchestrator.md")
	for _, required := range []string{
		"{{BRUNNR_CODE_ODD_SECTION:Organic Driven Development Is The Default Workflow (MANDATORY)}}",
		"{{BRUNNR_CODE_ODD_SECTION:Language Domain Contract}}",
		"{{BRUNNR_CODE_ODD_SECTION:Delegated Verification Gate (MANDATORY)}}",
		"Lossless Blocking Prompts", "Brunnr-Code Provider Defect Handoff",
		"Native Checking Contract", "Review Execution Contract", "Mandatory Delegation Triggers",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Claude orchestrator missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"{{BRUNNR_CODE_SDD_SECTION:", "SDD Workflow", "SDD Edit-Authority", "sdd-apply",
		"sdd-verify", "sdd-orchestrator-workflow.md", "optional SDD", "Optional SDD rule",
		"sdd-model-assignments", "SDD phase", "OpenSpec", "{{BRUNNR_CODE_RESEARCH_LIFECYCLE}}",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("Claude orchestrator retains %q", forbidden)
		}
	}
}

func TestCodexEmbeddedOrchestratorDoesNotPromiseUninstalledAssignments(t *testing.T) {
	content := MustRead("codex/orchestrator.md")
	if strings.Contains(content, "{{CODEX_ODD_ASSIGNMENTS}}") {
		t.Fatal("embedded Codex orchestrator has no installed renderer for ODD assignments")
	}
	if !strings.Contains(content, "worker classes, not installed named agents") {
		t.Fatal("Codex orchestrator must identify the delegation boundary")
	}
}

func TestPrimaryODDOnlyOrchestrator(t *testing.T) {
	for _, runtime := range []string{"codex", "generic"} {
		t.Run(runtime, func(t *testing.T) {
			content := MustRead(runtime + "/orchestrator.md")
			for _, required := range []string{
				"{{BRUNNR_CODE_ODD_SECTION:Organic Driven Development Is The Default Workflow (MANDATORY)}}",
				"{{BRUNNR_CODE_ODD_SECTION:Delegated Verification Gate (MANDATORY)}}",
				"Lossless Blocking Prompts", "Brunnr-Code Provider Defect Handoff",
				"Mandatory Delegation Triggers", "Native Checking Contract", "Review Execution Contract",
			} {
				if !strings.Contains(content, required) {
					t.Errorf("missing %q", required)
				}
			}
			for _, forbidden := range []string{
				"{{BRUNNR_CODE_SDD_SECTION:", "{{BRUNNR_CODE_RESEARCH_LIFECYCLE}}",
				"SDD Workflow", "SDD Edit-Authority", "SDD Session Preflight", "sdd-apply",
				"sdd-verify", "sdd-init", "sdd-model-assignments", "optional SDD",
				"Optional SDD rule", "SDD phase", "OpenSpec", "openspec", "{{CODEX_PHASE_EFFORTS}}",
			} {
				if strings.Contains(content, forbidden) {
					t.Errorf("retains %q", forbidden)
				}
			}
			if runtime == "codex" {
				for _, required := range []string{"Capability Check", "Blocking Delegation Contract", "Skill Loading for Delegation", "Graceful Degradation Path"} {
					if !strings.Contains(content, required) {
						t.Errorf("missing Codex contract %q", required)
					}
				}
			}
		})
	}
}

// retiredWorkRunCeremonyTokens enumerates the managed-WorkRun control-plane
// vocabulary that organic routing retires. Prompt assets are the one place this
// ceremony can outlive its Go source, because nothing compiles them — so every
// token is checked against every orchestrator instead of a sample.
var retiredWorkRunCeremonyTokens = []string{
	"work-capabilities",
	"work-start",
	"work-advance",
	"work-route",
	"work-status",
	"work-transition",
	"work-reconcile",
	"work-verification-decide",
	"WorkRun",
	"authorizedTransition",
	"Capability stop rule",
	"connectorSessionRef",
	"BRUNNR_CODE_PRODUCTIVE_RUNTIME",
	"{{BRUNNR_CODE_RUNTIME_AGENT_ID}}",
	"--contract gentle-ai.work-",
}

func TestODDOrchestratorsCarryNoRetiredWorkRunCeremony(t *testing.T) {
	paths := allODDOrchestratorAssetPaths(t)

	for _, path := range paths {
		// Fold case so a re-cased reintroduction ("workrun", "WORK-START")
		// cannot slip past the guard.
		content := strings.ToLower(MustRead(path))
		for _, token := range retiredWorkRunCeremonyTokens {
			t.Run(path+"#"+token, func(t *testing.T) {
				if strings.Contains(content, strings.ToLower(token)) {
					t.Fatalf("%s retains retired WorkRun ceremony token %q", path, token)
				}
			})
		}
	}
}

func TestOrchestratorsProjectOrganicRouting(t *testing.T) {
	paths := allODDOrchestratorAssetPaths(t)

	for _, path := range paths {
		content := MustRead(path)
		for _, required := range []string{
			"Mandatory Delegation Triggers",
			"Evidence budget rule", "one parallel batch",
			"Mapping rule", "one read-only explorer",
			"Write rule", "delegate a writer only for a named reason",
			"parallel writers follow the **Parallel writers** rule under `## Implementation Routing`",
			"Context rule", "reading that prepares a write", "broad research",
			"Mandatory Delegation Triggers", "delegated direct",
		} {
			if !strings.Contains(strings.ToLower(content), strings.ToLower(required)) {
				t.Fatalf("%s missing organic routing/native authority contract %q", path, required)
			}
		}
		for _, retired := range []string{
			"#### Review Lens Selection", "run exactly ONE lens", "run the full 4R set",
			"review/start(target)", "gentle-ai.review-integration/v1 --next-transition",
		} {
			if strings.Contains(content, retired) {
				t.Fatalf("%s retained prompt-owned review ceremony %q", path, retired)
			}
		}
		// Writers are delegated for a reason, and parallel
		// writers follow one rule instead of a single-writer ban.
		for _, retired := range []string{
			"a large task delegates one writer per task", "one writer per task",
			"Use a single writer thread", "Preserve one writer thread",
			"Keep one writer and", "Keep one writer.", "Keep one writer;",
		} {
			if strings.Contains(content, retired) {
				t.Fatalf("%s retained size-based or single-writer routing %q", path, retired)
			}
		}

		delegationHeading := "### Delegation Rules"
		if path == "codex/orchestrator.md" {
			delegationHeading = "## General Delegation Rules (Always Active)"
		}
		start := strings.Index(content, delegationHeading)
		end := strings.Index(content, "#### Mandatory Delegation Triggers")
		if start < 0 || end <= start {
			t.Fatalf("%s missing bounded general delegation section", path)
		}
		delegation := content[start:end]
		for _, required := range []string{"delegated direct", "does this inflate"} {
			if !strings.Contains(strings.ToLower(delegation), strings.ToLower(required)) {
				t.Fatalf("%s general delegation section missing route-neutral clause %q", path, required)
			}
		}
		for _, forbidden := range []string{
			"4+ files) | — | ✅ `sdd-explore`",
			"4+ files) | — | ✅ run as sdd-explore",
			"multiple files, new logic) | — | ✅ run as sdd-apply",
			"tests, builds, installs | — | ✅ `sdd-verify`",
			"Phase boundaries are not optional",
		} {
			if strings.Contains(delegation, forbidden) {
				t.Fatalf("%s general delegation section routes ordinary work through SDD %q", path, forbidden)
			}
		}
	}
}

func TestAllShippedOrchestratorsKeepDeliveryUnmanaged(t *testing.T) {
	const ordinaryDelivery = "Commit, push, PR, direct-main, emergency, and release gates are informational and unmanaged; ordinary repository policy decides delivery and they never reopen review for unchanged content."
	const receiptValidation = "Commit, push, PR, direct-main, emergency, and release gates validate the same exact owner-issued receipt/authorization"

	for _, path := range allSDDOrchestratorAssetPaths(t) {
		content := MustRead(path)
		if !strings.Contains(content, ordinaryDelivery) {
			t.Fatalf("%s does not leave delivery to ordinary repository policy", path)
		}
		if strings.Contains(content, receiptValidation) {
			t.Fatalf("%s retains receipt-gated delivery guidance", path)
		}
	}
}

func TestOrchestratorsRejectDelegationBypassLanguage(t *testing.T) {
	contents := make(map[string]string)
	for _, path := range allODDOrchestratorAssetPaths(t) {
		contents[path] = MustRead(path)
	}
	for path, content := range contents {
		for _, forbidden := range []string{
			"MUST delegate, complete the required fresh review/audit",
			"why delegation would be unsafe or wasteful",
			"delegate one writer or continue inline only if",
			"pause and delegate instead of silently continuing monolithically",
			"delegate a writer, or require a fresh review",
		} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s contains delegation bypass wording %q", path, forbidden)
			}
		}

		contentWords := normalizedWords(content)
		for _, forbidden := range []string{
			"delegate a writer or require a fresh review",
		} {
			if strings.Contains(contentWords, normalizedWords(forbidden)) {
				t.Fatalf("%s contains equivalent delegation bypass wording %q", path, forbidden)
			}
		}
	}

	codex := contents["codex/orchestrator.md"]
	for _, forbidden := range []string{
		"## Solo Path (default)",
		"Run each SDD phase inline, in dependency order, without spawning sub-agents",
		"fall back to the **Solo path**",
		"complete it inline",
	} {
		if strings.Contains(codex, forbidden) {
			t.Fatalf("codex/orchestrator.md contains solo-path bypass wording %q", forbidden)
		}
	}
	for _, want := range []string{
		"### Blocking Delegation Contract",
		"Codex sub-agents MUST be treated as waited handoffs, not fire-and-forget background jobs.",
		"`wait_agent` for every spawned agent in that batch",
		"Parallel does not mean background",
		"## Graceful Degradation Path (tooling unavailable only)",
	} {
		if !strings.Contains(codex, want) {
			t.Fatalf("codex/orchestrator.md missing guarded degradation wording %q", want)
		}
	}
	for _, forbidden := range []string{
		"both `spawn_agent` calls before either `wait_agent`",
	} {
		if strings.Contains(codex, forbidden) {
			t.Fatalf("codex/orchestrator.md contains fire-and-forget delegation wording %q", forbidden)
		}
	}
}

// allODDOrchestratorAssetPaths discovers the shipped top-level runtime prompts.
// Requiring the known runtime roots prevents an accidentally empty glob or a
// dropped agent from silently narrowing the negative guards.
func allODDOrchestratorAssetPaths(t *testing.T) []string {
	t.Helper()
	paths, err := fs.Glob(FS, "*/orchestrator.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{"claude", "codex", "generic"} {
		path := runtime + "/orchestrator.md"
		if !slices.Contains(paths, path) {
			t.Errorf("missing retained ODD orchestrator %s", path)
		}
	}
	if len(paths) == 0 {
		t.Fatal("no retained ODD orchestrator assets found")
	}
	return paths
}

func normalizedWords(s string) string {
	var b strings.Builder
	lastWasSpace := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastWasSpace = false
			continue
		}

		if !lastWasSpace {
			b.WriteByte(' ')
			lastWasSpace = true
		}
	}

	return strings.TrimSpace(b.String())
}

// TestAllEmbeddedAssetsAreReadable checks every shipped asset through the
// public reader and pins the ODD routing and review payloads that must survive.
func TestAllEmbeddedAssetsAreReadable(t *testing.T) {
	for _, path := range append(allODDOrchestratorAssetPaths(t),
		"skills/_shared/odd-orchestrator-sections.md",
		"skills/_shared/review-ledger-contract.md",
		"claude/agents/review-risk.md",
		"skills/go-testing/SKILL.md",
	) {
		if _, err := fs.Stat(FS, path); err != nil {
			t.Errorf("retained ODD/review payload %s is missing: %v", path, err)
		}
	}
	count := 0
	err := fs.WalkDir(FS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		count++
		t.Run(path, func(t *testing.T) {
			content, err := Read(path)
			if err != nil {
				t.Fatalf("Read(%q) error = %v", path, err)
			}
			if len(strings.TrimSpace(content)) == 0 || len(content) < 50 {
				t.Fatalf("Read(%q) returned empty or suspiciously short content (%d bytes)", path, len(content))
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("embedded asset inventory is empty")
	}
}

func TestClaudeEmbeddedAssetLayout(t *testing.T) {
	entries, err := FS.ReadDir("claude")
	if err != nil {
		t.Fatalf("ReadDir(claude) error = %v", err)
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Name()] = true
	}

	for _, name := range []string{"agents", "persona-neutral-residual.md", "orchestrator.md"} {
		if !seen[name] {
			t.Fatalf("claude embedded assets missing %q", name)
		}
	}
	// engram-protocol.md moved to the canonical engram/protocol.md asset
	// (design.md Decision 3) — it MUST NOT ship a stale duplicate under claude/.
	if seen["engram-protocol.md"] {
		t.Fatal("claude embedded assets must not ship a stale engram-protocol.md — content now lives in engram/protocol.md")
	}

	if seen["commands"] {
		t.Fatal("claude embedded assets still ship retired SDD commands")
	}

	agentEntries, err := FS.ReadDir("claude/agents")
	if err != nil {
		t.Fatalf("ReadDir(claude/agents) error = %v", err)
	}
	if len(agentEntries) != 8 {
		t.Fatalf("claude agents count = %d, want 8 retained review/Judgment Day agents", len(agentEntries))
	}
}

// TestEngramEmbeddedAssetLayout verifies the canonical protocol asset
// directory introduced by the consolidation (design.md Decision 3).
func TestEngramEmbeddedAssetLayout(t *testing.T) {
	entries, err := FS.ReadDir("engram")
	if err != nil {
		t.Fatalf("ReadDir(engram) error = %v", err)
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Name()] = true
	}

	if !seen["protocol.md"] {
		t.Fatal("engram embedded assets missing \"protocol.md\"")
	}
}

func TestFourRReviewAgentAssets(t *testing.T) {
	reviewAgents := []string{"review-risk", "review-readability", "review-reliability", "review-resilience"}
	nativeDirs := []string{"claude/agents"}
	agentRules := map[string][]string{
		"review-risk": {
			"Rule sources: ai-course-2 slides",
			"Flag when secrets, tokens, API keys, JWT secrets, or DB URLs are hardcoded",
			"Block when authz is enforced only in the frontend",
			"Do not flag when React default escaping is used",
		},
		"review-readability": {
			"Rule sources: ai-course-2 slides",
			"Flag magic numbers that should be named constants",
			"Flag long parameter lists that should be parameter objects",
			"Do not flag a small helper or inline constant",
		},
		"review-reliability": {
			"Rule sources: ai-course-2 slides",
			"Block behavior changes without tests that assert externally visible contract",
			"Block when CI can pass with `test.only`",
			"Do not flag intentional reliance on built-in async waiting/trace visibility",
		},
		"review-resilience": {
			"Rule sources: ai-course-2 slides",
			"Flag failures with no fallback, retry, or graceful-degradation path",
			"prod error rate > 1% investigate, > 2% emergency, > 5% all hands",
			"Do not flag explicitly low-impact expected issues",
		},
	}

	for _, dir := range nativeDirs {
		for _, agent := range reviewAgents {
			content := MustRead(dir + "/" + agent + ".md")
			for _, want := range []string{"read-only reviewer", "severity: BLOCKER | CRITICAL | WARNING | SUGGESTION", "No findings."} {
				if !strings.Contains(content, want) {
					t.Fatalf("%s/%s.md missing %q", dir, agent, want)
				}
			}
			for _, want := range agentRules[agent] {
				if !strings.Contains(content, want) {
					t.Fatalf("%s/%s.md missing concrete 4R rule %q", dir, agent, want)
				}
			}
		}
	}
}

func TestRetiredSDDLedgerGuidanceAbsentFromDistributedAssets(t *testing.T) {
	paths := []string{"skills/work-unit-commits/SKILL.md"}
	for _, path := range paths {
		content := strings.ToLower(MustRead(path))
		for _, retired := range []string{"sdd-tasks", "sdd relationship", "review ledger contract", "openspec/changes/", "sdd/{change-name}/review-ledger"} {
			if strings.Contains(content, retired) {
				t.Errorf("%s retains retired guidance %q", path, retired)
			}
		}
	}
	skill := MustRead("skills/work-unit-commits/SKILL.md")
	for _, retained := range []string{"## PR Relationship", "## ODD Relationship", "400", "Budget is not code-golf"} {
		if !strings.Contains(skill, retained) {
			t.Errorf("work-unit skill lost %q", retained)
		}
	}
}

func TestClaudeManagedOutputStylesAnchorReplyLanguageToLatestUserRequest(t *testing.T) {
	tests := []struct {
		path              string
		artifactContracts []string
	}{
		{
			path: "claude/output-style-neutral.md",
			artifactContracts: []string{
				"This output style governs direct replies to the user only.",
				"Generated technical artifacts default to English",
			},
		},
	}

	languageGuardrails := []string{
		"Determine the reply language from the latest actual user request",
		"not from Engram or memory context, repository/project language, tool output, previous assistant turns",
		"For mixed-language prompts, use the dominant language of the user's direct request.",
		"Quoted text, filenames, project names, isolated borrowed words",
		`phrases like "the Spanish part" do not switch the reply language by themselves.`,
		"If the selected reply language is English, every part of the direct reply must be English: greetings, interjections, acknowledgements, transition phrases, and the first sentence.",
		"Do not use Hola, dale, listo, Spanish punctuation, or other Spanish fragments.",
		"Prompts starting with or dominated by hi, hello, hey, or similar English greetings are English prompts unless the user explicitly asks for another language.",
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			content := MustRead(tc.path)

			for _, required := range languageGuardrails {
				if !strings.Contains(content, required) {
					t.Fatalf("%s missing language-drift guardrail %q", tc.path, required)
				}
			}

			for _, required := range tc.artifactContracts {
				if !strings.Contains(content, required) {
					t.Fatalf("%s lost artifact-language contract %q", tc.path, required)
				}
			}
		})
	}
}

// TestPersonasContainContextualSkillLoadingDirective verifies that every
// persona asset injected into a host's system prompt carries the mandatory
// "Contextual Skill Loading" directive (design Decisions 1 and 2 of the
// contextual-skill-loading change). The hardcoded "Skills (Auto-load based
// on context)" table MUST be removed at the same time.
//
// Claude variant references the native `Skill` tool by name. Non-Claude
// variants instruct the model to read the matching SKILL.md using their
// agent's read mechanism, since they have no Skill tool.
func TestPersonasContainContextualSkillLoadingDirective(t *testing.T) {
	tests := []struct {
		path      string
		isClaude  bool
		invokeMsg string // wording specific to the agent family
	}{
		{path: "generic/persona-neutral.md", isClaude: false, invokeMsg: "read the matching SKILL.md"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			content := MustRead(tc.path)

			// The competing hardcoded table MUST be gone.
			if strings.Contains(content, "## Skills (Auto-load based on context)") {
				t.Errorf("%s still contains the hardcoded `## Skills (Auto-load based on context)` table — must be replaced by the contextual directive", tc.path)
			}
			if strings.Contains(content, "| Context | Read this file |") {
				t.Errorf("%s still contains the hardcoded skill trigger table header — must be replaced by the contextual directive", tc.path)
			}

			// The new directive MUST be present.
			for _, required := range []string{
				"## Contextual Skill Loading (MANDATORY)",
				"<available_skills>",
				"Self-check BEFORE every response",
				"blocking requirement",
			} {
				if !strings.Contains(content, required) {
					t.Errorf("%s missing required directive substring %q", tc.path, required)
				}
			}

			// Claude variant references the Skill tool; non-Claude variants
			// instruct the model to read SKILL.md directly.
			if !strings.Contains(content, tc.invokeMsg) {
				t.Errorf("%s missing agent-specific invocation phrasing %q", tc.path, tc.invokeMsg)
			}
			if tc.isClaude {
				if !strings.Contains(content, "`Skill` tool") {
					t.Errorf("claude variant must name the `Skill` tool: %s", tc.path)
				}
			} else {
				// Non-Claude personas must NOT reference the Skill tool — that
				// would mislead users on agents that lack it.
				if strings.Contains(content, "`Skill` tool") {
					t.Errorf("non-Claude variant must not reference the `Skill` tool: %s", tc.path)
				}
			}
		})
	}
}

// TestMustReadPanicsOnMissingFile verifies that MustRead panics for a
// nonexistent file, confirming the safety mechanism works.
func TestMustReadPanicsOnMissingFile(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustRead() did not panic for missing file")
		}
	}()

	MustRead("nonexistent/file.md")
}

// TestEmbeddedAssetCount verifies we have the expected number of embedded files.
// This catches accidental deletions of asset files.
func TestEmbeddedAssetCount(t *testing.T) {
	// Count skill files.
	entries, err := FS.ReadDir("skills")
	if err != nil {
		t.Fatalf("ReadDir(skills) error = %v", err)
	}

	skillDirs := 0
	for _, entry := range entries {
		if entry.IsDir() {
			skillDirs++
		}
	}

	// Only retained skills with embedded files count; deleted SDD directories
	// contain no embedded assets.
	if skillDirs != 14 {
		t.Fatalf("expected 14 retained skill directories, got %d", skillDirs)
	}

	// Verify each skill directory has a SKILL.md.
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == "_shared" {
			for _, sharedFile := range []string{"README.md", "persistence-contract.md", "engram-convention.md", "odd-orchestrator-sections.md", "review-ledger-contract.md", "research-lifecycle.md", "skill-resolver.md"} {
				sharedPath := "skills/_shared/" + sharedFile
				if _, err := Read(sharedPath); err != nil {
					t.Fatalf("shared directory missing %q: %v", sharedFile, err)
				}
			}
			continue
		}
		skillPath := "skills/" + entry.Name() + "/SKILL.md"
		if _, err := Read(skillPath); err != nil {
			t.Fatalf("skill directory %q missing SKILL.md: %v", entry.Name(), err)
		}
	}
}

func TestODDOrchestratorsDoNotRequireRetiredRuntimeAttempts(t *testing.T) {
	for _, path := range allODDOrchestratorAssetPaths(t) {
		content := MustRead(path)
		for _, retired := range []string{"sdd-attempt acquire", "sdd-attempt settle", "Native Runtime Attempt Authority"} {
			if strings.Contains(content, retired) {
				t.Errorf("%s retains %q", path, retired)
			}
		}
	}
}

func TestODDOrchestratorsProjectNativeCheckingWithoutPromptOwnedLenses(t *testing.T) {
	for _, path := range allODDOrchestratorAssetPaths(t) {
		content := MustRead(path)
		section := markdownSection(content, "#### Native Checking Contract")
		if section == "" {
			t.Fatalf("%s missing Native Checking Contract", path)
		}
		for _, required := range []string{
			"Native RAR owns verification applicability",
			"bounded zero/one/four-lens plan",
			"never select lenses or author PASS",
			"passive ordinary document or image",
			"structural readback",
			"trivial passive documentation-only edit",
			"structural readback is the complete proportional check",
			"do not open a separate semantic-verification or heavy review ceremony",
			"applicable verifier is unavailable",
			"preserve the typed unavailable result",
			"never invent PASS, retry indefinitely, or escalate into extra ceremony",
			"quick check runs once",
			"Long or very-long work gets one cost/side-effect forecast",
			"Needs your decision",
			"Functional proof and adversarial review both project as **Checking**",
			"at most one scoped correction",
			"never reopen review for unchanged content",
		} {
			if !strings.Contains(section, required) {
				t.Fatalf("%s native checking contract missing %q", path, required)
			}
		}
		for _, retired := range []string{
			"Review Lens Selection", "review-risk", "review-readability",
			"review-reliability", "review-resilience", "loop-until-dry",
		} {
			if strings.Contains(content, retired) {
				t.Fatalf("%s retained prompt-owned review mechanism %q", path, retired)
			}
		}
	}
}

func markdownSection(content, heading string) string {
	start := strings.Index(content, heading)
	if start == -1 {
		return ""
	}
	section := content[start:]
	end := len(section)
	for _, levelHeading := range []string{"\n#### ", "\n### ", "\n## "} {
		if next := strings.Index(section[len(heading):], levelHeading); next != -1 {
			end = min(end, len(heading)+next)
		}
	}
	return section[:end]
}

func TestODDOrchestratorAssetsScopedToParent(t *testing.T) {
	for _, assetPath := range allODDOrchestratorAssetPaths(t) {
		t.Run(assetPath, func(t *testing.T) {
			content := MustRead(assetPath)
			if !strings.Contains(content, "Bind this to") || !strings.Contains(content, "Do NOT apply it to") {
				t.Fatalf("%s must scope its orchestrator instructions away from workers", assetPath)
			}
		})
	}
}

// #1731 parity: the shared persistence contract
// delegate for a named reason, never for file count, and forward the routing
// block's test-first policy instead of a strict TDD mode.
func TestDelegationSkillsDelegateForReasonAndForwardTestDiscipline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path     string
		required []string
		retired  []string
	}{
		{
			path:     "skills/_shared/persistence-contract.md",
			required: []string{"the applicable test-first policy and runner from `## Implementation Routing`"},
			retired:  []string{"TDD mode", "strict TDD"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			body := MustRead(tt.path)
			for _, want := range tt.required {
				if !strings.Contains(body, want) {
					t.Errorf("%s is missing %q", tt.path, want)
				}
			}
			for _, retired := range tt.retired {
				if strings.Contains(body, retired) {
					t.Errorf("%s keeps retired %q", tt.path, retired)
				}
			}
		})
	}
}
