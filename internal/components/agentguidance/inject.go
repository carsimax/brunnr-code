package agentguidance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/carsimax/brunnr-code/internal/agents"
	"github.com/carsimax/brunnr-code/internal/components/filemerge"
	"github.com/carsimax/brunnr-code/internal/model"
)

// RoutingSectionID is the managed marker section that owns routing guidance.
// It is deliberately independent of other managed sections so that installing
// or removing optional assets can never add or drop routing guidance.
const RoutingSectionID = "agent-routing"

// ErrInvalidTarget rejects an unusable installation root before any write, so
// a caller that lost its resolved home directory fails loudly instead of
// writing guidance into an unexpected location.
var ErrInvalidTarget = errors.New("invalid routing guidance target directory")

// ErrUnreadableSettings fails closed when an adapter's settings document cannot
// be decoded. Routing guidance is worth less than a user's configuration, so a
// document we cannot understand is never rewritten from an empty base.
var ErrUnreadableSettings = errors.New("unreadable agent settings document")

// ErrUnloadableGuidance fails closed when guidance would be written to a place
// the agent does not actually load. Silently installing unread guidance is the
// exact failure this component exists to prevent.
var ErrUnloadableGuidance = errors.New("routing guidance target is not loaded by the agent")

// Result mirrors the shape returned by the other component injectors so they
// can be aggregated by the same caller.
type Result struct {
	Changed bool
	Files   []string
}

// RoutingOptions supplies a caller-resolved settings path for adapters whose
// guidance is delivered through a managed orchestrator definition. The adapter
// retains its normal targetDir-derived path when SettingsPath is empty.
//
// ReviewContract renders the native review execution contract embedded in the
// orchestrator of a receipt-driven development runtime. It takes precedence
// over the package-level fallback (SetReviewContractSource); installers must
// always set it.
type RoutingOptions struct {
	ReviewContract              ReviewContractSource
	CodexPhaseModelAssignments  map[string]string
	CodexModelAssignments       map[string]model.CodexEffort
	CodexCarrilModelAssignments map[string]string
	// ClaudeGlobalModules opts the user-global Claude install into a core
	// CLAUDE.md plus on-demand modules under its config directory. Only a
	// caller installing the global scope may set it; every other agent and
	// the default keep the monolithic delivery.
	ClaudeGlobalModules bool
}

// InjectRoutingWithOptions installs the organic routing guidance for one
// supported agent under targetDir, which is the installation root the adapter
// resolves its configuration paths from.
//
// Delivery is strategy-aware: writing one markdown file for every adapter would
// land the guidance in a scope the agent never loads, or inside a template its
// own installer rewrites from an embedded asset on the next sync.
//
// Every runtime also receives its orchestrator instructions as a second
// managed section placed ahead of routing (see RenderOrchestrator).
//
// Only the marked sections are owned by Brunnr-Code: everything a user wrote
// around them is preserved verbatim, and a second identical injection is a no-op.
func InjectRoutingWithOptions(targetDir string, agent model.AgentID, options RoutingOptions) (Result, error) {
	// Render before resolving the delivery so an unsupported agent is rejected
	// without having touched the filesystem.
	rendered, err := RenderRouting(agent)
	if err != nil {
		return Result{}, err
	}
	if agent == model.AgentCodex {
		rendered += "\n\n### Codex ODD worker assignments\n\nUse the exact model and reasoning_effort for the selected worker class when calling `spawn_agent`; set `fork_turns: \"none\"` for overrides. These assignments apply to ODD delegation, not native RDD review.\n\n" +
			model.RenderCodexODDAssignments(options.CodexPhaseModelAssignments, options.CodexModelAssignments, options.CodexCarrilModelAssignments)
	}

	delivery, err := resolveRoutingDelivery(targetDir, agent, options)
	if err != nil {
		return Result{}, err
	}

	// Keep the boundary in the unconditional guidance carrier, independent of
	// persona selection. Nesting inside the owned routing section also
	// preserves it when other component writers retain that section.
	rendered = InjectRemoteAuthorization(rendered)

	// The orchestrator travels with routing into the same always-loaded scope
	// and the same write, so the two can never land in different files or be
	// half-applied.
	// With Claude global modules that scope receives the core, which keeps
	// every critical policy, and the modules it points at.
	var orchestrator string
	var modules []orchestratorModule
	switch {
	case delivery.kind == deliveryClaudeModules:
		// Pointers name the absolute directory of the planned module paths.
		bundle, err := buildOrchestratorModules(agent, options.ReviewContract, filepath.Dir(delivery.paths[1]))
		if err != nil {
			return Result{}, err
		}
		orchestrator, modules = bundle.core, bundle.modules
	default:
		orchestrator, err = RenderOrchestratorWithSource(agent, options.ReviewContract)
		if err != nil {
			return Result{}, err
		}
	}
	merge := func(existing string) string {
		if orchestrator != "" {
			existing = injectOrchestratorSection(existing, orchestrator)
		}
		return filemerge.InjectMarkdownSection(existing, RoutingSectionID, rendered)
	}

	switch delivery.kind {
	case deliveryClaudeModules:
		return commitOrchestratorCore(filepath.Dir(delivery.paths[0]), merge, modules)
	default:
		return injectPromptSection(delivery, merge)
	}
}

// RoutingPathsWithOptions reports the exact filesystem paths
// InjectRoutingWithOptions would write for one supported agent under
// targetDir, including any caller-resolved effective settings path, without
// creating or touching anything.
//
// Install and sync must snapshot every file they are about to rewrite. Routing
// guidance is delivered outside the component loop, so a selection whose
// components do not happen to cover the same file would otherwise be rewritten
// without a backup and could never be rolled back. Both answers come from the
// same delivery resolution, so the backup contract cannot drift away from what
// the injector actually writes.
func RoutingPathsWithOptions(targetDir string, agent model.AgentID, options RoutingOptions) ([]string, error) {
	delivery, err := resolveRoutingDelivery(targetDir, agent, options)
	if err != nil {
		return nil, err
	}
	return delivery.paths, nil
}

// routingDeliveryKind names the scopes an agent actually loads guidance
// from. Writing one markdown file for every adapter would land the guidance
// where the agent never reads it, or inside a template its own installer
// rewrites on the next sync.
type routingDeliveryKind int

const (
	deliveryPromptSection routingDeliveryKind = iota
	// deliveryClaudeModules is the opt-in Claude global core plus modules;
	// its paths are the core, every known module name and the ledger.
	deliveryClaudeModules
)

// routingDelivery is the single resolution of how and where guidance reaches
// one agent. It is intentionally pure so the path reporter and the injector can
// share it without the reporter gaining write side effects.
type routingDelivery struct {
	kind    routingDeliveryKind
	adapter agents.Adapter
	paths   []string
}

// resolveRoutingDelivery selects the delivery strategy and its target paths.
//
// It fails closed on anything that would make the guidance unreachable, because
// an unreachable target is exactly the failure this component exists to
// prevent — and reporting a path the injector cannot write is the same defect
// seen from the backup side.
func resolveRoutingDelivery(targetDir string, agent model.AgentID, options RoutingOptions) (routingDelivery, error) {
	if strings.TrimSpace(targetDir) == "" {
		return routingDelivery{}, fmt.Errorf("%w: %q", ErrInvalidTarget, targetDir)
	}

	adapter, err := agents.NewAdapter(agent)
	if err != nil {
		return routingDelivery{}, fmt.Errorf("resolve routing guidance adapter for %q: %w", agent, err)
	}

	switch {
	case agent == model.AgentClaudeCode && options.ClaudeGlobalModules:
		configDir := adapter.GlobalConfigDir(targetDir)
		if !filepath.IsAbs(configDir) {
			return routingDelivery{}, fmt.Errorf("%w: Claude global modules need an absolute config dir, got %q", ErrInvalidTarget, configDir)
		}
		return routingDelivery{kind: deliveryClaudeModules, adapter: adapter, paths: orchestratorCorePaths(configDir)}, nil

	default:
		promptPath := adapter.SystemPromptFile(targetDir)
		if strings.TrimSpace(promptPath) == "" {
			return routingDelivery{}, fmt.Errorf("%w: adapter %q exposes no system prompt file", ErrInvalidTarget, agent)
		}
		return routingDelivery{kind: deliveryPromptSection, adapter: adapter, paths: []string{promptPath}}, nil
	}
}

// guidanceMerge folds every managed guidance section into existing content.
type guidanceMerge func(existing string) string

// injectPromptSection is the default delivery: managed marker sections inside
// the adapter's own system prompt file.
func injectPromptSection(delivery routingDelivery, merge guidanceMerge) (Result, error) {
	promptPath := delivery.paths[0]

	existing, err := readFileOrEmpty(promptPath)
	if err != nil {
		return Result{}, err
	}

	updated := merge(existing)

	writeResult, err := filemerge.WriteFileAtomic(promptPath, []byte(updated), filemerge.ExistingFileMode(promptPath, 0o644))
	if err != nil {
		return Result{}, err
	}

	return Result{Changed: writeResult.Changed, Files: []string{promptPath}}, nil
}

// readFileOrEmpty treats a missing prompt file as empty content: the first
// install legitimately has nothing to merge into.
func readFileOrEmpty(path string) (string, error) {
	data, err := readBytesOrEmpty(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readBytesOrEmpty(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	return data, nil
}
