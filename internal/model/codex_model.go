package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// codexModelCatalog is Brunnr-Code's curated selectable Codex model catalog for
// per-phase custom assignments. It is a UI/configuration catalog, not a runtime
// availability probe; the Codex CLI remains the source of truth at execution
// time. Order is intentional: newest/most-capable first.
var codexModelCatalog = []string{
	"gpt-6.1-astra",
	"gpt-6.1-sol",
	"gpt-6.1-luna",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
}

// ValidCodexReviewModel accepts a single CLI model identifier, including IDs
// discovered from Codex that are not yet in the bundled catalog. Rejecting
// whitespace and option prefixes keeps persisted state from shaping argv.
func ValidCodexReviewModel(id string) bool {
	if id == "" || id[0] == '-' {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// CodexAvailableModels returns Brunnr-Code's curated selectable Codex model
// catalog for per-phase Custom picker assignments. The slice is a copy —
// mutations do not affect the canonical catalog.
func CodexAvailableModels() []string {
	out := make([]string, len(codexModelCatalog))
	copy(out, codexModelCatalog)
	return out
}

var codexModelDiscoveryTimeout = 3 * time.Second

const codexModelDiscoveryOutputLimit = 1 << 20

var codexLookPath = exec.LookPath
var codexCommand = exec.CommandContext

type codexDiscoveryOutput struct {
	data     strings.Builder
	limit    int
	overflow bool
}

func (w *codexDiscoveryOutput) Write(p []byte) (int, error) {
	remaining := w.limit - w.data.Len()
	if remaining <= 0 {
		w.overflow = true
		return 0, io.ErrShortWrite
	}
	if len(p) > remaining {
		_, _ = w.data.Write(p[:remaining])
		w.overflow = true
		return remaining, io.ErrShortWrite
	}
	return w.data.Write(p)
}

type codexDiscoveredModelCatalog struct {
	Models []codexCatalogModel `json:"models"`
}

type codexCatalogModel struct {
	Slug                     string `json:"slug"`
	Visibility               string `json:"visibility"`
	SupportedInAPI           *bool  `json:"supported_in_api"`
	SupportedReasoningLevels []struct {
		Effort string `json:"effort"`
	} `json:"supported_reasoning_levels"`
	// ServiceTiers is nil when an older Codex omits the key entirely.
	ServiceTiers *[]CodexServiceTier `json:"service_tiers"`
}

// CodexServiceTier is one Codex service tier a model advertises. ID is the
// request value written to config.toml's top-level service_tier key; "fast"
// is the priority tier, a speed selection rather than a reasoning effort.
type CodexServiceTier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CodexModelCapabilities is what the installed Codex runtime advertises for
// one model: its reasoning efforts and its service tiers.
type CodexModelCapabilities struct {
	Efforts      []CodexEffort
	ServiceTiers []CodexServiceTier
	// ServiceTiersReported is false when the runtime predates service_tiers;
	// the tiers are then unknown and a persisted selection must be kept.
	ServiceTiersReported bool
}

// CodexModelCatalog is the Custom picker catalog. Models missing from
// Capabilities (including the whole curated fallback) have unknown
// capabilities, so callers keep their curated effort list for them.
type CodexModelCatalog struct {
	Models       []string
	Capabilities map[string]CodexModelCapabilities
}

// DiscoverCodexModels returns selectable models and their advertised
// capabilities from the locally installed Codex CLI. It falls back to the
// curated catalog if Codex is unavailable, times out, returns invalid JSON,
// or reports no selectable models.
func DiscoverCodexModels(ctx context.Context) CodexModelCatalog {
	fallback := CodexModelCatalog{Models: CodexAvailableModels()}
	discoveryCtx, cancel := context.WithTimeout(ctx, codexModelDiscoveryTimeout)
	defer cancel()

	codexPath, err := codexLookPath("codex")
	if err != nil {
		return fallback
	}
	cmd := codexCommand(discoveryCtx, codexPath, "debug", "models")
	output := &codexDiscoveryOutput{limit: codexModelDiscoveryOutputLimit}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil || output.overflow {
		return fallback
	}

	var catalog codexDiscoveredModelCatalog
	if err := json.Unmarshal([]byte(output.data.String()), &catalog); err != nil {
		return fallback
	}

	discovered := CodexModelCatalog{Models: make([]string, 0, len(catalog.Models))}
	seen := make(map[string]struct{}, len(catalog.Models))
	for _, entry := range catalog.Models {
		slug := strings.TrimSpace(entry.Slug)
		if slug == "" || (entry.Visibility != "" && entry.Visibility != "list") || (entry.SupportedInAPI != nil && !*entry.SupportedInAPI) {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		discovered.Models = append(discovered.Models, slug)
		if capabilities, ok := entry.capabilities(); ok {
			if discovered.Capabilities == nil {
				discovered.Capabilities = make(map[string]CodexModelCapabilities)
			}
			discovered.Capabilities[slug] = capabilities
		}
	}
	if len(discovered.Models) == 0 {
		return fallback
	}
	return discovered
}

// capabilities keeps only efforts Brunnr-Code can route, in canonical order, and
// service tiers that are safe request values. "default" is Codex's explicit
// standard-routing sentinel, not a tier.
func (entry codexCatalogModel) capabilities() (CodexModelCapabilities, bool) {
	var capabilities CodexModelCapabilities
	advertised := make(map[CodexEffort]bool, len(entry.SupportedReasoningLevels))
	for _, level := range entry.SupportedReasoningLevels {
		advertised[CodexEffort(level.Effort)] = true
	}
	for _, effort := range codexEffortOrder {
		if advertised[effort] {
			capabilities.Efforts = append(capabilities.Efforts, effort)
		}
	}
	if entry.ServiceTiers != nil {
		capabilities.ServiceTiersReported = true
	}
	for _, tier := range ptrValue(entry.ServiceTiers) {
		if tier.ID != "default" && ValidCodexServiceTier(tier.ID) && !slices.ContainsFunc(capabilities.ServiceTiers, func(t CodexServiceTier) bool { return t.ID == tier.ID }) {
			capabilities.ServiceTiers = append(capabilities.ServiceTiers, tier)
		}
	}
	return capabilities, len(capabilities.Efforts) > 0 || capabilities.ServiceTiersReported
}

func ptrValue[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}

// ValidCodexServiceTier accepts a single lowercase request token so persisted
// state cannot shape config.toml beyond one string value.
func ValidCodexServiceTier(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// FilterCodexModelList returns the subset of models whose ID contains query as a
// case-insensitive substring. An empty query returns a copy of models.
func FilterCodexModelList(models []string, query string) []string {
	if strings.TrimSpace(query) == "" {
		return append([]string(nil), models...)
	}
	q := strings.ToLower(query)
	out := make([]string, 0, len(models))
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), q) {
			out = append(out, m)
		}
	}
	return out
}

// CodexEffort represents an OpenAI reasoning_effort level used for Codex
// per-phase delegation via spawn_agent.
type CodexEffort string

const (
	CodexEffortLow    CodexEffort = "low"
	CodexEffortMedium CodexEffort = "medium"
	CodexEffortHigh   CodexEffort = "high"
	CodexEffortXHigh  CodexEffort = "xhigh"
	// Max and Ultra exist only on models whose runtime catalog advertises them.
	CodexEffortMax   CodexEffort = "max"
	CodexEffortUltra CodexEffort = "ultra"
)

// codexEffortOrder is the canonical low-to-high order of routable efforts.
var codexEffortOrder = []CodexEffort{CodexEffortLow, CodexEffortMedium, CodexEffortHigh, CodexEffortXHigh, CodexEffortMax, CodexEffortUltra}

// Valid reports whether the effort value is a known routable level.
func (e CodexEffort) Valid() bool {
	return slices.Contains(codexEffortOrder, e)
}

type CodexCarrilDefault struct {
	Model  string
	Effort CodexEffort
}

type CodexPresetKey string

const (
	CodexPresetLowCost     CodexPresetKey = "low-cost"
	CodexPresetRecommended CodexPresetKey = "recommended"
	CodexPresetPowerful    CodexPresetKey = "powerful"
)

// codexODDRoles is the render order of the ODD worker classes. The class
// names are also the keys of codexPresetMatrix and of persisted custom
// model assignments.
var codexODDRoles = []string{"odd-explorer", "odd-worker", "odd-verify"}

// codexPresetMatrix is the default model and effort per ODD worker class.
var codexPresetMatrix = map[CodexPresetKey]map[string]CodexCarrilDefault{
	CodexPresetLowCost: {
		"odd-explorer": {Model: "gpt-6.1-luna", Effort: CodexEffortHigh},
		"odd-worker":   {Model: "gpt-6.1-luna", Effort: CodexEffortMedium},
		"odd-verify":   {Model: "gpt-6.1-sol", Effort: CodexEffortMedium},
	},
	CodexPresetRecommended: {
		"odd-explorer": {Model: "gpt-6.1-luna", Effort: CodexEffortHigh},
		"odd-worker":   {Model: "gpt-6.1-luna", Effort: CodexEffortHigh},
		"odd-verify":   {Model: "gpt-6.1-sol", Effort: CodexEffortMedium},
	},
	CodexPresetPowerful: {
		"odd-explorer": {Model: "gpt-6.1-luna", Effort: CodexEffortHigh},
		"odd-worker":   {Model: "gpt-6.1-sol", Effort: CodexEffortHigh},
		"odd-verify":   {Model: "gpt-6.1-astra", Effort: CodexEffortXHigh},
	},
}

// codexPresetOrchestrator is the main-session model per preset. It is no
// longer one shared policy: the low-cost preset runs the orchestrator on
// Luna, because a Plus plan cannot afford Sol in both the main session and
// every strong lane, and the strong lanes are where reasoning actually pays.
// Unknown keys fall back to Recommended, as the carril matrix does.
var codexPresetOrchestrator = map[CodexPresetKey]CodexOrchestratorAssignment{
	CodexPresetLowCost:     {Model: "gpt-6.1-luna", Effort: CodexEffortMedium},
	CodexPresetRecommended: {Model: "gpt-6.1-sol", Effort: CodexEffortMedium},
	CodexPresetPowerful:    {Model: "gpt-6.1-astra", Effort: CodexEffortMedium},
}

// CodexODDEffortsForPreset returns the per-role effort assignments of a
// preset. Unknown keys fall back to Recommended.
func CodexODDEffortsForPreset(preset string) map[string]CodexEffort {
	defaults, ok := codexPresetMatrix[CodexPresetKey(preset)]
	if !ok {
		defaults = codexPresetMatrix[CodexPresetRecommended]
	}
	out := make(map[string]CodexEffort, len(defaults))
	for role, value := range defaults {
		out[role] = value.Effort
	}
	return out
}

// RenderCodexODDAssignments provides spawn_agent arguments for ODD work.
// RDD assignments remain persisted for the native adapter, not prompt delegation.
func RenderCodexODDAssignments(phaseModels map[string]string, efforts map[string]CodexEffort, carrilModels map[string]string) string {
	defaults := codexPresetMatrix[CodexPresetRecommended]
	var b strings.Builder
	b.WriteString("| ODD worker class | Model | reasoning_effort |\n|---|---|---|\n")
	for _, role := range codexODDRoles {
		modelID := carrilModels[role]
		if modelID == "" {
			modelID = defaults[role].Model
		}
		if phaseModels[role] != "" {
			modelID = phaseModels[role]
		}
		effort := efforts[role]
		if !effort.Valid() {
			effort = defaults[role].Effort
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` |\n", role, modelID, effort)
	}
	return b.String()
}

// CodexOrchestratorAssignment is the explicit top-level Codex session model
// selected by a Brunnr-Code preset. It is separate from the delegated ODD roles.
type CodexOrchestratorAssignment struct {
	Model  string
	Effort CodexEffort
}

// CodexPresetOrchestratorAssignment returns the main-session policy for a
// named preset. Every preset runs the orchestrator at medium effort: it plans,
// routes and adjudicates rather than doing the delegated work, so low effort
// made it the weakest link in the chain while medium keeps it responsive and
// still routes correctly. The model does vary — see codexPresetOrchestrator.
// Unknown keys intentionally fall back to Recommended.
func CodexPresetOrchestratorAssignment(preset string) *CodexOrchestratorAssignment {
	assignment, ok := codexPresetOrchestrator[CodexPresetKey(preset)]
	if !ok {
		assignment = codexPresetOrchestrator[CodexPresetRecommended]
	}
	return &assignment
}

// CodexPresetCarrilDefaults returns a defensive copy of the selected preset's
// carril defaults. The string boundary preserves compatibility with persisted
// state; unknown keys intentionally fall back to Recommended.
func CodexPresetCarrilDefaults(preset string) map[string]CodexCarrilDefault {
	defaults, ok := codexPresetMatrix[CodexPresetKey(preset)]
	if !ok {
		defaults = codexPresetMatrix[CodexPresetRecommended]
	}
	out := make(map[string]CodexCarrilDefault, len(defaults))
	for carril, value := range defaults {
		out[carril] = value
	}
	return out
}

// CodexCarrilModelsForPreset returns the model portion of a preset's carril
// defaults. Unknown persisted keys inherit the Recommended fallback policy.
func CodexCarrilModelsForPreset(preset string) map[string]string {
	defaults := CodexPresetCarrilDefaults(preset)
	out := make(map[string]string, len(defaults))
	for carril, value := range defaults {
		out[carril] = value.Model
	}
	return out
}
