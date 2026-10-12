package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Two retired SDD files were rendered by Go code, with no golden of either:
//   - Codex's sdd-{strong,mid,cheap}.config.toml profiles (v1.36.0 to
//     v3.7.0), upserted by codex.WriteCodexProfiles into whatever the file
//     held, so a render depends on the state the previous release left;
//   - Claude Code's lazy skills/_shared/sdd-orchestrator-workflow.md
//     (v1.43.3 to v3.7.0), written by writeClaudeLazySDDWorkflow. Until
//     v2.6.0 it carried the user's model assignments: it is replayed with
//     none and with every model preset the release offered, so a table the
//     user customized stays unproven.
//
// The generator replays each release's own writer: it extracts the release's
// internal tree, adds a test that calls the exact expression the release used,
// and runs it offline (GOPROXY=off, GOTOOLCHAIN=local, -mod=readonly). The
// local module cache must already hold the release's dependencies; when it
// does not, the generator exits with replayUnavailableExit instead of
// fetching them. Releases whose render inputs are
// byte-identical share one replay. Codex profiles are replayed from absence
// and from every render an earlier release produced, so every upgrade path
// between releases is covered.
const (
	codexProfilesFile = "internal/agents/codex/profiles.go"
	claudeModelFile   = "internal/model/claude_model.go"
	claudeWorkflowKey = "skills/_shared/sdd-orchestrator-workflow.md"
	replayTestFile    = "zz_gentle_ai_replay_test.go"
	// replayUnavailableExit is the exit code for a replay whose modules are
	// missing from the local cache, distinct from a generation failure.
	replayUnavailableExit = 3
	offlineModuleMiss     = "module lookup disabled by GOPROXY=off"
)

var (
	sessionPreflight    = "prompt, lazyWorkflow, workflows, err := prepareSessionPreflight(homeDir, adapter, opts)"
	codexWriterArgs     = map[string]string{"func WriteCodexProfiles(codexHomeDir string) (": "", "func WriteCodexProfiles(codexHomeDir string, assignments []ProfileAssignment) (": ", nil"}
	codexProfilePaths   = "func SddProfilePaths(codexHomeDir string) []string"
	modulePath          = regexp.MustCompile(`(?m)^module (\S+)$`)
	maxCodexReplayRound = 5

	claudeWriter           = "func writeClaudeLazySDDWorkflow("
	claudeAssignmentWriter = "func writeClaudeLazySDDWorkflow(homeDir string, adapter agents.Adapter, legacyAssignments map[string]model.ClaudeModelAlias, phaseAssignments map[string]model.ClaudePhaseAssignment) (InjectionResult, error) {"
	claudePlainWriter      = "func writeClaudeLazySDDWorkflow(homeDir string, adapter agents.Adapter) (InjectionResult, error) {"
	claudePreparedWriter   = "func writeClaudeLazySDDWorkflow(homeDir string, adapter agents.Adapter, prepared ...string) (InjectionResult, error) {"
	// Releases with the prepared writer passed it the lazy workflow their
	// session preflight rendered with no options.
	claudePreparedCall = "writeClaudeLazySDDWorkflow(homeDir, adapter, lazyWorkflow)"
	claudeLazyRender   = "lazy, err = renderClaudeSessionPreflight()"
	claudePreset       = regexp.MustCompile(`(?m)^func (ClaudeModelPreset[A-Za-z0-9]+)\(\) map\[string\]ClaudeModelAlias \{$`)
)

type replayGroup struct {
	tags []string
	// kind is the render a group replays: claude or codex.
	kind string
	pkg  string
	// test is the replay test source with the module path still to fill in.
	test string
	dir  string
	// outputs maps an output file name to every distinct render.
	outputs map[string][]string
}

type replayHarvest struct {
	claude, codex []*replayGroup
	byKey         map[string]*replayGroup
	tmp           string
}

func newReplayHarvest() *replayHarvest {
	return &replayHarvest{byKey: map[string]*replayGroup{}}
}

// collect classifies one release's Claude Code workflow and Codex profile
// writers.
func (h *replayHarvest) collect(tag string, files map[string]string) {
	if blob, ok := files[sddInjectFile]; ok && strings.Contains(cachedBlob(blob), claudeWriter) {
		h.collectClaude(tag, files, cachedBlob(blob))
	}
	if blob, ok := files[codexProfilesFile]; ok {
		h.collectCodex(tag, cachedBlob(blob))
	}
}

// collectClaude replays the release's writeClaudeLazySDDWorkflow with every
// argument its installer passed for an install without custom assignments.
func (h *replayHarvest) collectClaude(tag string, files map[string]string, inject string) {
	var calls []string
	helper := ""
	switch {
	case strings.Contains(inject, claudeAssignmentWriter):
		blob, ok := files[claudeModelFile]
		presets := claudePreset.FindAllStringSubmatch(cachedBlob(blob), -1)
		if !ok || len(presets) == 0 {
			fail("%s: no Claude model presets in %s; update the generator", tag, claudeModelFile)
		}
		calls = append(calls, "writeClaudeLazySDDWorkflow(home, adapter, nil, nil)")
		for _, preset := range presets {
			calls = append(calls, "writeClaudeLazySDDWorkflow(home, adapter, model."+preset[1]+"(), nil)")
		}
	case strings.Contains(inject, claudePlainWriter):
		calls = append(calls, "writeClaudeLazySDDWorkflow(home, adapter)")
	case strings.Contains(inject, claudePreparedWriter):
		if strings.Count(inject, claudePreparedCall) != 1 || strings.Count(inject, sessionPreflight) != 1 || strings.Count(inject, claudeLazyRender) != 1 {
			fail("%s: the Claude Code workflow is not the session preflight render; update the generator", tag)
		}
		calls = append(calls, "gentleAIReplayPrepared(home, adapter)")
		helper = claudeReplayPrepared
	default:
		fail("%s: the Claude Code workflow writer is not recognized; update the generator", tag)
	}
	var b strings.Builder
	for _, call := range calls {
		fmt.Fprintf(&b, "\t\tfunc(home string) (InjectionResult, error) { return %s },\n", call)
	}
	h.group("claude", tag, "./internal/components/sdd", fmt.Sprintf(claudeReplayTest, b.String(), helper), "internal/components", "internal/assets", "internal/model", "internal/agents")
}

func (h *replayHarvest) collectCodex(tag, source string) {
	if !strings.Contains(source, "func WriteCodexProfiles(") {
		return // v4.0.0 keeps only the path inventory
	}
	args, found := "", false
	for signature, call := range codexWriterArgs {
		if strings.Contains(source, signature) {
			args, found = call, true
		}
	}
	if !found || !strings.Contains(source, codexProfilePaths) {
		fail("%s: the Codex profile writer is not recognized; update the generator", tag)
	}
	h.group("codex", tag, "./internal/agents/codex", fmt.Sprintf(codexReplayTest, args), "internal/agents/codex", "internal/components/filemerge", "internal/model")
}

// group files tag under the replay of its render inputs: the test and every
// tree the writer reads.
func (h *replayHarvest) group(kind, tag, pkg, test string, trees ...string) {
	args := []string{"rev-parse", tag + ":go.mod"}
	for _, tree := range trees {
		args = append(args, tag+":"+tree)
	}
	key := kind + "\n" + pkg + "\n" + test + "\n" + git(args...)
	if g := h.byKey[key]; g != nil {
		g.tags = append(g.tags, tag)
		return
	}
	g := &replayGroup{tags: []string{tag}, kind: kind, pkg: pkg, test: test}
	h.byKey[key] = g
	switch kind {
	case "claude":
		h.claude = append(h.claude, g)
	default:
		h.codex = append(h.codex, g)
	}
}

// groups lists every replay group: Claude Code, then Codex.
func (h *replayHarvest) groups() []*replayGroup {
	return slices.Concat(h.claude, h.codex)
}

// run replays every group: Claude Code once, Codex until its
// renders are closed under every upgrade.
func (h *replayHarvest) run() {
	var err error
	if h.tmp, err = os.MkdirTemp("", "gen-sdd-replay-"); err != nil {
		fail("replay workspace: %v", err)
	}
	cleanups = append(cleanups, func() { _ = os.RemoveAll(h.tmp) })
	// A stable directory per group keeps the Go build cache warm across
	// Codex rounds; each run extracts it and removes it again.
	for i, g := range h.groups() {
		g.dir = filepath.Join(h.tmp, strconv.Itoa(i))
	}
	parallel(h.claude, func(g *replayGroup) { g.replay(nil) })
	// Groups are in first-release order. Each writer rewrites absence, every
	// render an earlier release left, and its own renders until they are
	// stable. Downgrades are not modeled: a v1.36.0 writer appends a blank
	// line to a later render on every run, so their renders are unbounded,
	// and such a file is preserved and reported instead.
	var states []string
	seen := map[string]bool{}
	for _, g := range h.codex {
		inputs := append([]string(nil), states...)
		for round := 1; ; round++ {
			if round > maxCodexReplayRound {
				fail("%s: Codex profile renders did not stabilize after %d rounds", g.tags[0], maxCodexReplayRound)
			}
			g.replay(inputs)
			grown := false
			for _, name := range sortedKeys(g.outputs) {
				for _, render := range g.outputs[name] {
					if !slices.Contains(inputs, render) {
						inputs = append(inputs, render)
						grown = true
					}
				}
			}
			if !grown {
				break
			}
		}
		for _, name := range sortedKeys(g.outputs) {
			for _, render := range g.outputs[name] {
				if !seen[render] {
					seen[render] = true
					states = append(states, render)
				}
			}
		}
	}
}

// replay runs the group's test with inputs (the Codex states to rewrite) and
// records every render it writes.
func (g *replayGroup) replay(inputs []string) {
	extract(g.tags[0], g.dir)
	defer os.RemoveAll(g.dir)
	module := modulePath.FindStringSubmatch(readFile(filepath.Join(g.dir, "go.mod")))
	if module == nil {
		fail("%s: go.mod declares no module", g.tags[0])
	}
	test := strings.ReplaceAll(g.test, "{{MODULE}}", module[1])
	if err := os.WriteFile(filepath.Join(g.dir, filepath.FromSlash(strings.TrimPrefix(g.pkg, "./")), replayTestFile), []byte(test), 0o644); err != nil {
		fail("write replay test: %v", err)
	}
	in, out := filepath.Join(g.dir, "replay-in"), filepath.Join(g.dir, "replay-out")
	for _, dir := range []string{in, out} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail("create %s: %v", dir, err)
		}
	}
	for i, input := range inputs {
		if err := os.WriteFile(filepath.Join(in, strconv.Itoa(i+1)), []byte(input), 0o644); err != nil {
			fail("write replay input: %v", err)
		}
	}
	cmd := exec.Command("go", "test", "-count=1", "-vet=off", "-run", "^TestGentleAIReplay$", g.pkg)
	cmd.Dir = g.dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOWORK=off", "GENTLE_AI_REPLAY_IN="+in, "GENTLE_AI_REPLAY_OUT="+out)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), offlineModuleMiss) {
			exit(replayUnavailableExit, "%s: replay %s needs modules missing from the local module cache, and replays run offline (GOPROXY=off); fill the cache once with network access (for example `go mod download` in a checkout of %s), then rerun\n%s", g.tags[0], g.pkg, g.tags[0], output)
		}
		fail("%s: replay %s: %v\n%s", g.tags[0], g.pkg, err, output)
	}
	if g.outputs == nil {
		g.outputs = map[string][]string{}
	}
	err := filepath.WalkDir(out, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		render := readFile(path)
		if name := entry.Name(); !slices.Contains(g.outputs[name], render) {
			g.outputs[name] = append(g.outputs[name], render)
		}
		return nil
	})
	if err != nil || len(g.outputs) == 0 {
		fail("%s: replay %s wrote no render: %v", g.tags[0], g.pkg, err)
	}
}

// register adds every render to the asset registry in release order.
func (h *replayHarvest) register(tags []string, assets *assetHarvest) {
	groupOf := map[string]*replayGroup{}
	for _, g := range h.groups() {
		for _, tag := range g.tags {
			groupOf[g.kind+tag] = g
		}
	}
	for _, tag := range tags {
		if g := groupOf["claude"+tag]; g != nil {
			for _, render := range g.outputs["claude"] {
				assets.add(claudeWorkflowKey, render, tag)
			}
		}
		if g := groupOf["codex"+tag]; g != nil {
			for _, name := range sortedKeys(g.outputs) {
				if !strings.HasPrefix(name, "sdd-") || !strings.HasSuffix(name, ".config.toml") {
					fail("%s: unexpected Codex profile %s", tag, name)
				}
				for _, render := range g.outputs[name] {
					assets.add("profiles/"+name, render, tag)
				}
			}
		}
	}
	fmt.Fprintf(os.Stderr, "gen-sdd-agent-digests: replayed %d Claude Code workflow and %d Codex profile writers\n", len(h.claude), len(h.codex))
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func parallel(groups []*replayGroup, run func(*replayGroup)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(4, runtime.NumCPU()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				run(groups[i])
			}
		}()
	}
	for i := range groups {
		next <- i
	}
	close(next)
	wg.Wait()
}

// extract writes tag's module files and internal tree, without tests, to dir.
func extract(tag, dir string) {
	archive := gitBytes("archive", "--format=tar", tag, "--", "go.mod", "go.sum", "internal", ":(exclude)*_test.go")
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			fail("%s: read archive: %v", tag, err)
		}
		path := filepath.Join(dir, filepath.FromSlash(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path, 0o755)
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				var data []byte
				if data, err = io.ReadAll(reader); err == nil {
					err = os.WriteFile(path, data, 0o644)
				}
			}
		}
		if err != nil {
			fail("%s: extract %s: %v", tag, header.Name, err)
		}
	}
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		fail("read %s: %v", path, err)
	}
	return string(data)
}

const claudeReplayTest = `package sdd

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"{{MODULE}}/internal/agents/claude"
	"{{MODULE}}/internal/model"
)

var _ model.AgentID

// TestGentleAIReplay runs every writer call in a fresh home and records the
// lazy SDD workflow it wrote.
func TestGentleAIReplay(t *testing.T) {
	out := os.Getenv("GENTLE_AI_REPLAY_OUT")
	adapter := claude.NewAdapter()
	for i, write := range []func(home string) (InjectionResult, error){
%s	} {
		home := t.TempDir()
		if _, err := write(home); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(adapter.SkillsDir(home), "_shared", "sdd-orchestrator-workflow.md"))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(out, strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "claude"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
%s`

const claudeReplayPrepared = `
// gentleAIReplayPrepared passes the writer the workflow the session
// preflight rendered, as Inject did.
func gentleAIReplayPrepared(home string, adapter *claude.Adapter) (InjectionResult, error) {
	_, lazy, _, err := prepareSessionPreflight(home, adapter, InjectOptions{})
	if err != nil {
		return InjectionResult{}, err
	}
	return writeClaudeLazySDDWorkflow(home, adapter, lazy)
}
`

const codexReplayTest = `package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestGentleAIReplay writes the profiles from absence (input 0) and over
// every numbered input state.
func TestGentleAIReplay(t *testing.T) {
	in, out := os.Getenv("GENTLE_AI_REPLAY_IN"), os.Getenv("GENTLE_AI_REPLAY_OUT")
	for i := 0; ; i++ {
		dir := t.TempDir()
		if i > 0 {
			data, err := os.ReadFile(filepath.Join(in, strconv.Itoa(i)))
			if os.IsNotExist(err) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range SddProfilePaths(dir) {
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, _, err := WriteCodexProfiles(dir%s); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(out, strconv.Itoa(i))
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, path := range SddProfilePaths(dir) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, filepath.Base(path)), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
`
