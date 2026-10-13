package cli

import (
	"github.com/carsimax/brunnr-code/internal/components/engram"
	"github.com/carsimax/brunnr-code/internal/model"
	"github.com/carsimax/brunnr-code/internal/state"
)

// restoreCodexServiceTier selects the tier state recorded as written, which is
// also the tier Brunnr-Code manages. Invalid persisted values are dropped and
// never used to edit config.toml.
func restoreCodexServiceTier(selection *model.Selection, persisted state.InstallState) {
	tier := persisted.CodexServiceTier
	if !model.ValidCodexServiceTier(tier) {
		tier = ""
	}
	selection.CodexServiceTier, selection.CodexManagedServiceTier = tier, tier
}

// noteCodexServiceTier keeps the service tier a global engram injection left
// in Codex's config.toml. Results that wrote no Codex config are ignored.
func (s *runtimeState) noteCodexServiceTier(result engram.InjectionResult) {
	if s != nil && result.CodexServiceTier != nil {
		s.codexServiceTier = result.CodexServiceTier
	}
}

// applyCodexServiceTier stores engram's write result as the managed tier in
// the same state write as the rest of the run, so a failure cannot leave one
// written without the other. nil means Codex config.toml was not written, so
// the previous value stays.
func applyCodexServiceTier(installState *state.InstallState, tier *string) {
	if tier != nil {
		installState.CodexServiceTier = *tier
	}
}
