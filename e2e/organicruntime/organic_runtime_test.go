// Package organicruntime_test proves the journeys a configured agent actually
// performs once Brunnr-Code stopped owning implementation: the agent implements
// organically, and Brunnr-Code's authority begins only after a candidate exists.
//
// Every assertion here is driven through the real brunnr-code binary and the real
// `review` command surface against real Git repositories and a real bare remote.
// There is no runtime fixture, no TLS control plane, and no bearer session: the
// retired control plane cannot be proven, only the shipped product can.
package organicruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carsimax/brunnr-code/internal/reviewerprovider"
)

const (
	// realAgentE2EEnvironment gates the real-agent journeys.
	realAgentE2EEnvironment = "BRUNNR_CODE_REAL_AGENT_E2E"

	organicLocalTimeout     = 90 * time.Second
	organicSetupTimeout     = 5 * time.Minute
	organicAgentTimeout     = 4 * time.Minute
	organicCommandWaitDelay = 10 * time.Second

	// organicWithdrawalDeadline replaces the retired short-TTL harness. The
	// surviving authorization has no wall-clock lifetime: it is withdrawn by an
	// explicit user action instead of expiring, so the harness that used to sleep
	// out a TTL now performs the withdrawal instantly. The deadline only keeps CI
	// honest — a journey that needs longer than this has stopped being a journey.
	organicWithdrawalDeadline = 60 * time.Second
)

// Actor process contract. The delegated worker must be a real, separate OS
// process rather than an in-test closure, because the behaviour under test is
// exactly what a sub-agent does on its own: it implements and commits, and it
// never escalates its own route. Re-executing the compiled test binary keeps
// that real without adding a language runtime dependency to the suite.
const (
	organicActorRoleEnvironment                     = "BRUNNR_CODE_ORGANIC_ACTOR_ROLE"
	organicActorRepoEnvironment                     = "BRUNNR_CODE_ORGANIC_ACTOR_REPO"
	organicActorPathEnvironment                     = "BRUNNR_CODE_ORGANIC_ACTOR_PATH"
	organicActorBodyEnvironment                     = "BRUNNR_CODE_ORGANIC_ACTOR_BODY"
	organicActorMessageEnvironment                  = "BRUNNR_CODE_ORGANIC_ACTOR_MESSAGE"
	organicActorBinaryEnvironment                   = "BRUNNR_CODE_ORGANIC_ACTOR_BINARY"
	organicTestBinaryEnvironment                    = "BRUNNR_CODE_ORGANIC_TEST_BINARY"
	organicProviderCaptureFakeAgentEnvironment      = "BRUNNR_CODE_ORGANIC_PROVIDER_CAPTURE_FAKE_AGENT"
	organicProviderCaptureFakePayloadEnvironment    = "BRUNNR_CODE_ORGANIC_PROVIDER_CAPTURE_FAKE_PAYLOAD"
	organicProviderCaptureFakeFailureEnvironment    = "BRUNNR_CODE_ORGANIC_PROVIDER_CAPTURE_FAKE_FAILURE"
	organicProviderCaptureFakeInvocationEnvironment = "BRUNNR_CODE_ORGANIC_PROVIDER_CAPTURE_FAKE_INVOCATION"

	organicActorRoleDirect    = "direct"
	organicActorRoleDelegated = "delegated"

	organicDirectActorMarker    = "ORGANIC_DIRECT_CANDIDATE_COMMITTED"
	organicDelegatedActorMarker = "ORGANIC_DELEGATED_CANDIDATE_COMMITTED"
)

// Wire vocabulary. These are literals on purpose: an end-to-end test pins the
// contract the product emits, it does not re-derive it from the packages that
// emit it.
const (
	organicRiskLow    = "low"
	organicRiskMedium = "medium"
	organicRiskHigh   = "high"

	organicStateApproved           = "approved"
	organicStateValidating         = "validating"
	organicStateCorrectionRequired = "correction_required"

	organicGateSchema = "brunnr-code.review-gate-result/v1"
	organicModeSchema = "brunnr-code.review-mode/v1"

	organicGateAllow = "allow"
	organicModeOff   = "off"
	organicModeOn    = "on"
)

var organicBinary string

func TestMain(m *testing.M) {
	if agent := strings.TrimSpace(os.Getenv(organicProviderCaptureFakeAgentEnvironment)); agent != "" {
		os.Exit(runOrganicProviderCaptureFake(agent))
	}
	if role := strings.TrimSpace(os.Getenv(organicActorRoleEnvironment)); role != "" {
		os.Exit(runOrganicActor(role))
	}
	if binary := strings.TrimSpace(os.Getenv(organicTestBinaryEnvironment)); binary != "" {
		resolvedBinary, err := exec.LookPath(binary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s=%q does not resolve to an executable: %v\n", organicTestBinaryEnvironment, binary, err)
			os.Exit(1)
		}
		organicBinary = resolvedBinary
		os.Exit(m.Run())
	}
	workspace, err := os.MkdirTemp("", "organic-e2e-binary")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create organic binary workspace: %v\n", err)
		os.Exit(1)
	}
	binary, err := buildOrganicBinary(workspace)
	if err != nil {
		_ = os.RemoveAll(workspace)
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	organicBinary = binary
	code := m.Run()
	_ = os.RemoveAll(workspace)
	os.Exit(code)
}

// runOrganicActor is the implementation actor. It edits exactly one already
// understood file and explicitly creates the candidate commit, which is the only
// thing an organic actor owes: the provider never creates or guesses a commit.
func runOrganicActor(role string) int {
	repo := os.Getenv(organicActorRepoEnvironment)
	relative := os.Getenv(organicActorPathEnvironment)
	body := os.Getenv(organicActorBodyEnvironment)
	message := os.Getenv(organicActorMessageEnvironment)
	if repo == "" || relative == "" || message == "" {
		fmt.Fprintln(os.Stderr, "organic actor requires repository, path, and message")
		return 1
	}

	marker := organicDirectActorMarker
	if role == organicActorRoleDelegated {
		marker = organicDelegatedActorMarker
		// A delegated worker observes authority read-only. It must not start a
		// review, select a route, or promote itself: escalating its own
		// route is precisely the failure this journey exists to catch.
		if err := assertOrganicDelegatedWorkerStaysInRoute(repo); err != nil {
			fmt.Fprintf(os.Stderr, "delegated actor escalated its own route: %v\n", err)
			return 1
		}
	}

	target := filepath.Join(repo, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "organic actor mkdir: %v\n", err)
		return 1
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "organic actor write: %v\n", err)
		return 1
	}
	for _, arguments := range [][]string{
		{"add", "--", relative},
		{"commit", "-q", "-m", message},
	} {
		if _, err := organicGitOutput(context.Background(), repo, arguments...); err != nil {
			fmt.Fprintf(os.Stderr, "organic actor git: %v\n", err)
			return 1
		}
	}
	fmt.Print(marker)
	return 0
}

func assertOrganicDelegatedWorkerStaysInRoute(repo string) error {
	binary := os.Getenv(organicActorBinaryEnvironment)
	if binary == "" {
		return errors.New("delegated actor has no brunnr-code binary to observe authority with")
	}
	ctx, cancel := context.WithTimeout(context.Background(), organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(ctx, binary, "review", "mode", "status", "--cwd", repo, "--json")
	command.Env = os.Environ()
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read review mode: %w", err)
	}
	var mode organicModeResult
	if err := json.Unmarshal(output, &mode); err != nil {
		return fmt.Errorf("decode review mode: %w", err)
	}
	if mode.Schema != organicModeSchema || mode.Operation != "status" {
		return fmt.Errorf("delegated actor read an unexpected authority projection %#v", mode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Journeys
// ---------------------------------------------------------------------------

// TestOrganicDirectoryIdentityAcceptsCanonicalAliases keeps the repository
// selection boundary: relative, absolute, aliased, and `git -C` forms all denote
// exactly one repository, and a non-directory never does.
func TestOrganicDirectoryIdentityAcceptsCanonicalAliases(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	worktree := harness.repo.worktree

	canonical := harness.commonDir()
	relative := harness.git("rev-parse", "--git-common-dir")
	if !filepath.IsAbs(relative) {
		relative = filepath.Join(worktree, relative)
	}
	if !sameOrganicDirectory(canonical, relative) {
		t.Fatalf("relative and absolute common-dir forms denote different repositories: %q vs %q", relative, canonical)
	}

	parent := filepath.Dir(worktree)
	viaParent, err := organicGitOutput(context.Background(), parent, "-C", worktree, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if !sameOrganicDirectory(canonical, viaParent) {
		t.Fatalf("git -C form denotes a different repository: %q vs %q", viaParent, canonical)
	}

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(worktree, alias); err != nil {
		t.Skipf("directory aliases are unavailable: %v", err)
	}
	viaAlias, err := organicGitOutput(context.Background(), alias, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if !sameOrganicDirectory(canonical, viaAlias) {
		t.Fatalf("aliased worktree denotes a different repository: %q vs %q", viaAlias, canonical)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sameOrganicDirectory(canonical, file) {
		t.Fatal("a regular file was accepted as the repository directory")
	}
}

func TestClaudeProviderAdapterUsesPinnedNetworkNoneRuntime(t *testing.T) {
	if testing.Short() || os.Getenv("BRUNNR_CODE_CLAUDE_RUNTIME_E2E") != "1" {
		t.Skip("claude_network_none_skipped: requires the pinned Docker proof image")
	}
	binary := os.Getenv("BRUNNR_CODE_CLAUDE_RUNTIME_BINARY")
	if binary == "" {
		t.Fatal("claude_network_none_unavailable: pinned binary path is empty")
	}
	adapter := reviewerprovider.NewClaudeAdapter()
	originalLookPath := adapter.LookPath
	adapter.LookPath = func(string) (string, error) { return binary, nil }
	t.Cleanup(func() { adapter.LookPath = originalLookPath })
	response := []byte(`{"subject_hash":"local-subject","inspection":{"status":"completed","paths":["internal/provider/candidate.go"]},"lens":"reliability","findings":[],"evidence":["loopback inspected the frozen candidate"]}`)
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead && request.URL.Path == "/api/hello" {
			writer.WriteHeader(http.StatusOK)
			return
		}
		requests++
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			t.Errorf("Claude request = %s %s, want POST /v1/messages", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read Claude request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if !bytes.Contains(payload, []byte("provider transport")) {
			t.Error("Claude Messages request omitted the provider prompt")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_local\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		_, _ = fmt.Fprint(writer, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		encoded, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": string(response)}})
		_, _ = fmt.Fprintf(writer, "event: content_block_delta\ndata: %s\n\n", encoded)
		_, _ = fmt.Fprint(writer, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = fmt.Fprint(writer, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n")
		_, _ = fmt.Fprint(writer, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	t.Setenv("ANTHROPIC_API_KEY", "local-loopback-key")
	raw, err := adapter.Review(t.Context(), reviewerprovider.NewInvocation([]byte("provider transport")))
	if err != nil {
		t.Fatalf("pinned Claude adapter loopback: %v", err)
	}
	if want := append(response, '\n'); !bytes.Equal(raw, want) || requests == 0 {
		t.Fatalf("pinned Claude loopback = %q with %d Messages requests, want %q with at least one request", raw, requests, want)
	}
	completedRequests := requests
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if raw, err := adapter.Review(ctx, reviewerprovider.NewInvocation([]byte(`{"review":"provider transport"}`))); err == nil || len(raw) != 0 || requests != completedRequests {
		t.Fatalf("pinned Claude adapter failure = %q, %v; want no bytes and a transport error", raw, err)
	}
}

func TestCodexProviderAdapterUsesPinnedLocalRuntime(t *testing.T) {
	if testing.Short() || strings.TrimSpace(os.Getenv("BRUNNR_CODE_CODEX_RUNTIME_E2E")) != "1" {
		t.Skip("set BRUNNR_CODE_CODEX_RUNTIME_E2E=1 to run the pinned local Codex transport proof")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the Codex egress proof requires Linux strace")
	}
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Fatalf("Codex egress isolation unavailable: strace is required: %v", err)
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.147.0" {
		t.Fatalf("Codex version = %q, %v", strings.TrimSpace(string(version)), err)
	}
	harness := newOrganicHarness(t)
	baseTree := strings.TrimSpace(harness.git("rev-parse", "HEAD^{tree}"))
	harness.writeFiles(map[string]string{"internal/provider/candidate.go": "package provider\n\nfunc Value() int { return 1 }\n"})
	harness.git("add", "--", "internal/provider/candidate.go")
	harness.git("commit", "-qm", "feat: committed Codex correction candidate")
	const lineage = "codex-loopback-egress-proof"
	statusPayload := harness.run(
		"review", "status", "--cwd", harness.repo.worktree, "--contract", "brunnr-code.review-integration/v2",
		"--agent", "codex", "--lineage", lineage, "--base-ref", baseTree, "--committed-only", "--next-transition",
	)
	var negotiated organicProviderStatusResult
	if err := json.Unmarshal(statusPayload, &negotiated); err != nil || negotiated.NextTransition == nil || negotiated.NextTransition.Execute == nil {
		t.Fatalf("decode committed Codex START: %v\n%s", err, statusPayload)
	}
	start := negotiated.NextTransition.Execute
	stdout, stderr, err := harness.runAllowFailure(
		"review", "start", "--cwd", harness.repo.worktree, "--contract", "brunnr-code.review-integration/v2",
		"--target", start.argument("target"), "--projection", start.argument("projection"), "--base-ref", baseTree, "--committed-only",
		"--lineage", lineage, "--agent", "codex", "--consent", "granted", "--focus", "reliability",
	)
	if err != nil {
		t.Fatalf("committed Codex START: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var started organicStartResult
	if err := json.Unmarshal([]byte(stdout), &started); err != nil || started.State != "reviewing" || len(started.SelectedLenses) != 1 {
		t.Fatalf("committed Codex START = %#v, %v\n%s", started, err, stdout)
	}
	statusPayload = harness.run(
		"review", "status", "--cwd", harness.repo.worktree, "--contract", "brunnr-code.review-integration/v2",
		"--agent", "codex", "--lineage", lineage, "--base-ref", baseTree, "--committed-only", "--next-transition",
	)
	var reviewing organicProviderStatusResult
	if err := json.Unmarshal(statusPayload, &reviewing); err != nil {
		t.Fatalf("decode committed Codex capture STATUS: %v\n%s", err, statusPayload)
	}
	binding := organicProviderBinding(t, reviewing)
	order, err := strconv.Atoi(binding["order"])
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(map[string]any{
		"subject_hash": binding["subject-hash"],
		"inspection":   map[string]any{"status": "completed", "paths": []string{"internal/provider/candidate.go"}},
		"lens":         binding["lens"],
		"findings": []any{map[string]any{
			"location": "internal/provider/candidate.go:3", "severity": "CRITICAL",
			"claim":          "the committed candidate returns the wrong value",
			"proof_refs":     []string{"internal/provider/candidate.go:3 is introduced by the committed candidate"},
			"evidence_class": "deterministic", "causal_disposition": "introduced",
		}},
		"evidence": []string{"loopback found a candidate-caused blocker in the frozen committed candidate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rootRequests, modelRequests, responseRequests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/" {
			rootRequests++
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/v1/models" {
			modelRequests++
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"object":"list","data":[{"id":"gpt-5.6-terra","object":"model","created":0,"owned_by":"brunnr-code-loopback"}]}`)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
			t.Errorf("Codex request = %s %s, want GET /, GET /v1/models, or POST /v1/responses", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		responseRequests++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read Codex request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		// The severe deterministic finding reaches the refuter inside the
		// same compiled capture; it corroborates every claim it is asked about.
		if refuter := codexLoopbackRefuterResponse(body); refuter != nil {
			writeCodexResponsesLoopback(t, writer, refuter)
			return
		}
		writeCodexResponsesLoopback(t, writer, response)
	}))
	defer server.Close()

	proxy := newLoopbackDenyingProxy(t)
	defer proxy.Close()

	traceBase := filepath.Join(t.TempDir(), "codex-connect")
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "codex")
	if err := os.WriteFile(wrapper, []byte(`#!/bin/sh
set -eu
exec "$BRUNNR_CODE_RUNTIME_TRACE_BINARY" -ff -o "$BRUNNR_CODE_RUNTIME_TRACE_LOG" -e trace=connect "$BRUNNR_CODE_RUNTIME_TRACE_TARGET" "$@"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	environment := append(harness.environment(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BRUNNR_CODE_CODEX_REVIEWER_LOOPBACK_BASE_URL="+server.URL+"/v1",
		"BRUNNR_CODE_RUNTIME_TRACE_BINARY="+strace,
		"BRUNNR_CODE_RUNTIME_TRACE_LOG="+traceBase,
		"BRUNNR_CODE_RUNTIME_TRACE_TARGET="+binary,
		"HTTP_PROXY="+proxy.URL,
		"HTTPS_PROXY="+proxy.URL,
		"ALL_PROXY="+proxy.URL,
		"http_proxy="+proxy.URL,
		"https_proxy="+proxy.URL,
		"all_proxy="+proxy.URL,
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	)
	arguments := []string{
		"review", "capture-result", "--agent", "codex",
		"--repository-context", binding["repository-context"], "--expected-revision", binding["expected-revision"],
		"--lineage", binding["lineage"], "--target", binding["target"], "--lens", binding["lens"], "--order", strconv.Itoa(order),
	}
	stdout, stderr, err = runOrganicCommand(t, organicBinary, harness.repo.worktree, environment, arguments...)
	if err != nil {
		t.Fatalf("registered Codex provider route: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if responseRequests != 2 {
		t.Fatalf("registered Codex loopback made %d root, %d model, and %d Responses requests; want one reviewer and one refuter Responses request", rootRequests, modelRequests, responseRequests)
	}
	denied := proxy.deniedRequests()
	if denied == 0 {
		t.Fatal("Codex made no externally addressed request through the denying proxy; the external-refusal path was not exercised")
	}
	connections, err := codexTracedConnectAddresses(traceBase)
	if err != nil {
		t.Fatalf("Codex egress trace is unavailable: %v", err)
	}
	for _, address := range connections {
		if !address.IsLoopback() {
			t.Fatalf("Codex bypassed egress isolation with a non-loopback connect to %s", address)
		}
	}
	t.Logf("Codex egress proof: %d external attempts denied by the loopback proxy; strace observed %d Internet-socket connects, all loopback: %v", denied, len(connections), connections)
	var terminal struct {
		organicFinalizeResult
		StatusContinuation *organicProviderExecute `json:"status_continuation"`
	}
	if err := json.Unmarshal([]byte(stdout), &terminal); err != nil {
		t.Fatalf("decode terminal Codex provider capture: %v\n%s", err, stdout)
	}
	if terminal.Operation != "review/capture-result" || terminal.State != organicStateCorrectionRequired || terminal.StatusContinuation == nil {
		t.Fatalf("registered Codex provider capture did not open committed correction: %#v", terminal)
	}
	if terminal.StatusContinuation.Operation != "review.status" {
		t.Fatalf("Codex committed correction continuation operation = %q, want review.status", terminal.StatusContinuation.Operation)
	}
	wantTokens := map[string]bool{
		"--lineage=" + lineage: false, "--base-ref=" + baseTree: false,
		"--committed-only=true": false, "--agent=codex": false,
	}
	continuationArguments := []string{"review", "status"}
	for _, argument := range terminal.StatusContinuation.Arguments {
		continuationArguments = append(continuationArguments, argument.Token)
		if _, found := wantTokens[argument.Token]; found {
			wantTokens[argument.Token] = true
		}
	}
	for token, found := range wantTokens {
		if !found {
			t.Fatalf("Codex committed correction continuation omitted %q: %#v", token, terminal.StatusContinuation)
		}
	}
	continuationPayload := harness.run(continuationArguments...)
	var correction organicProviderStatusResult
	if err := json.Unmarshal(continuationPayload, &correction); err != nil {
		t.Fatalf("decode returned Codex correction STATUS: %v\n%s", err, continuationPayload)
	}
	if correction.Authority.LineageID != lineage || correction.Authority.State != organicStateCorrectionRequired ||
		correction.Projection.BaseTree != baseTree || correction.NextTransition == nil ||
		correction.NextTransition.ReasonCode != "correction_plan_required" {
		t.Fatalf("returned Codex committed correction STATUS = %#v", correction)
	}
}

// codexTracedConnectAddresses parses one trace per child process. strace -ff
// avoids interleaving unfinished connect calls from Codex's concurrent workers.
// Every Internet-socket connection is returned, including failed attempts, so
// a direct external bypass is rejected before it can be mistaken for isolation.
func codexTracedConnectAddresses(traceBase string) ([]netip.Addr, error) {
	paths, err := filepath.Glob(traceBase + ".*")
	if err != nil {
		return nil, fmt.Errorf("discover strace output: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("strace produced no child trace files")
	}
	sort.Strings(paths)
	addresses := make([]netip.Addr, 0)
	for _, path := range paths {
		trace, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read strace output %q: %w", path, err)
		}
		for _, line := range strings.Split(string(trace), "\n") {
			address, internet, err := codexTracedConnectAddress(line)
			if err != nil {
				return nil, fmt.Errorf("parse strace connect %q: %w", line, err)
			}
			if internet {
				addresses = append(addresses, address)
			}
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("strace observed no Internet-socket connects")
	}
	return addresses, nil
}

func codexTracedConnectAddress(line string) (netip.Addr, bool, error) {
	if !strings.Contains(line, "connect(") {
		return netip.Addr{}, false, nil
	}
	prefix := ""
	switch {
	case strings.Contains(line, "sa_family=AF_INET,"):
		prefix = `sin_addr=inet_addr("`
	case strings.Contains(line, "sa_family=AF_INET6,"):
		prefix = `sin6_addr=inet_pton(AF_INET6, "`
	default:
		return netip.Addr{}, false, nil
	}
	start := strings.Index(line, prefix)
	if start < 0 {
		return netip.Addr{}, false, errors.New("missing numeric address")
	}
	remaining := line[start+len(prefix):]
	end := strings.IndexByte(remaining, '"')
	if end < 0 {
		return netip.Addr{}, false, errors.New("unterminated numeric address")
	}
	address, err := netip.ParseAddr(remaining[:end])
	if err != nil {
		return netip.Addr{}, false, err
	}
	return address, true, nil
}

type loopbackDenyingProxy struct {
	*httptest.Server
	lock     sync.Mutex
	requests int
}

func newLoopbackDenyingProxy(t *testing.T) *loopbackDenyingProxy {
	t.Helper()
	proxy := &loopbackDenyingProxy{}
	proxy.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !denyingProxyTargetIsExternal(request) {
			forwarded := request.Clone(request.Context())
			forwarded.RequestURI = ""
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			response, err := transport.RoundTrip(forwarded)
			if err != nil {
				http.Error(writer, fmt.Sprintf("loopback proxy forward: %v", err), http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			for name, values := range response.Header {
				writer.Header()[name] = append([]string{}, values...)
			}
			writer.WriteHeader(response.StatusCode)
			_, _ = io.Copy(writer, response.Body)
			return
		}
		proxy.lock.Lock()
		proxy.requests++
		proxy.lock.Unlock()
		// The proxy is process-scoped through a command's environment. Any
		// attempted external request receives an explicit refusal instead of a
		// route to the host network.
		writer.WriteHeader(http.StatusForbidden)
	}))
	return proxy
}

func (proxy *loopbackDenyingProxy) deniedRequests() int {
	proxy.lock.Lock()
	defer proxy.lock.Unlock()
	return proxy.requests
}

// loopbackDenyingProxyEnvironment routes every proxied protocol of a child
// process through the loopback-denying proxy while loopback stays direct, so
// an external request is refused instead of reaching the host network.
func loopbackDenyingProxyEnvironment(proxy *loopbackDenyingProxy) []string {
	return []string{
		"HTTP_PROXY=" + proxy.URL,
		"HTTPS_PROXY=" + proxy.URL,
		"ALL_PROXY=" + proxy.URL,
		"http_proxy=" + proxy.URL,
		"https_proxy=" + proxy.URL,
		"all_proxy=" + proxy.URL,
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}
}

func denyingProxyTargetIsExternal(request *http.Request) bool {
	host := request.URL.Hostname()
	if host == "" {
		host = request.Host
		if hostname, _, err := net.SplitHostPort(host); err == nil {
			host = hostname
		}
	}
	host = strings.Trim(host, "[]")
	if host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err != nil || !address.IsLoopback()
}

var (
	codexLoopbackRequestHash = regexp.MustCompile(`request_hash\\*":\\*"(sha256:[0-9a-f]{64})`)
	codexLoopbackFindingID   = regexp.MustCompile(`finding_id\\*":\\*"([A-Za-z0-9-]+)`)
)

// codexLoopbackRefuterResponse answers a provider refuter request carried in
// a Responses body with a result that corroborates every claim, or returns
// nil when the body is not a refuter request.
func codexLoopbackRefuterResponse(body []byte) []byte {
	if !bytes.Contains(body, []byte("brunnr-code.review-provider-refuter-request/v1")) {
		return nil
	}
	hash := codexLoopbackRequestHash.FindSubmatch(body)
	if hash == nil {
		return nil
	}
	seen := map[string]bool{}
	results := []map[string]any{}
	for _, match := range codexLoopbackFindingID.FindAllSubmatch(body, -1) {
		id := string(match[1])
		if seen[id] {
			continue
		}
		seen[id] = true
		results = append(results, map[string]any{"finding_id": id, "outcome": "corroborated", "proof_refs": []string{"loopback reproduced the claim in the frozen candidate"}})
	}
	payload, err := json.Marshal(map[string]any{"refuter_request_hash": string(hash[1]), "results": results})
	if err != nil {
		return nil
	}
	return payload
}

func writeCodexResponsesLoopback(t *testing.T, writer http.ResponseWriter, response []byte) {
	t.Helper()
	writer.Header().Set("Content-Type", "text/event-stream")
	completed := map[string]any{
		"id":                 "resp_local",
		"object":             "response",
		"created_at":         0,
		"status":             "completed",
		"error":              nil,
		"incomplete_details": nil,
		"instructions":       nil,
		"model":              "gpt-5.6-terra",
		"output": []any{map[string]any{
			"id":     "msg_local",
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        string(response),
				"annotations": []any{},
			}},
		}},
		"parallel_tool_calls":  false,
		"previous_response_id": nil,
		"store":                false,
		"temperature":          1,
		"text":                 map[string]any{"format": map[string]string{"type": "text"}},
		"tool_choice":          "auto",
		"tools":                []any{},
		"top_p":                1,
		"truncation":           "disabled",
		"usage":                nil,
		"user":                 nil,
		"metadata":             map[string]any{},
	}
	created := maps.Clone(completed)
	created["status"] = "in_progress"
	created["output"] = []any{}
	for _, event := range []struct {
		name string
		data any
	}{
		{name: "response.created", data: map[string]any{"type": "response.created", "response": created}},
		{name: "response.output_item.added", data: map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_local", "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}}},
		{name: "response.content_part.added", data: map[string]any{"type": "response.content_part.added", "item_id": "msg_local", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}},
		{name: "response.output_text.delta", data: map[string]any{"type": "response.output_text.delta", "item_id": "msg_local", "output_index": 0, "content_index": 0, "delta": string(response)}},
		{name: "response.output_text.done", data: map[string]any{"type": "response.output_text.done", "item_id": "msg_local", "output_index": 0, "content_index": 0, "text": string(response)}},
		{name: "response.content_part.done", data: map[string]any{"type": "response.content_part.done", "item_id": "msg_local", "output_index": 0, "content_index": 0, "part": completed["output"].([]any)[0].(map[string]any)["content"].([]any)[0]}},
		{name: "response.output_item.done", data: map[string]any{"type": "response.output_item.done", "output_index": 0, "item": completed["output"].([]any)[0]}},
		{name: "response.completed", data: map[string]any{"type": "response.completed", "response": completed}},
	} {
		encoded, err := json.Marshal(event.data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event.name, encoded); err != nil {
			t.Fatal(err)
		}
	}
}

const organicReviewBindingPrefix = "BRUNNR_CODE_REVIEW_BINDING "

func TestNativeProviderCaptureResultCLIUsesCompiledAdapters(t *testing.T) {
	for _, test := range []struct {
		name   string
		agent  string
		binary string
	}{
		{name: "Claude", agent: "claude-code", binary: "claude"},
		{name: "Codex", agent: "codex", binary: "codex"},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := newOrganicHarness(t)
			harness.writeFiles(map[string]string{"internal/provider/candidate.go": "package provider\n\nfunc Value() int { return 1 }\n"})
			lineage := "provider-capture-" + test.agent
			start := organicProviderStart(t, harness, lineage, test.agent)
			if len(start.SelectedLenses) != 1 {
				t.Fatalf("selected lenses = %v, want one", start.SelectedLenses)
			}
			status := organicProviderStatus(t, harness, lineage, test.agent)
			binding := organicProviderBinding(t, status)
			bin := t.TempDir()
			fake := organicWriteProviderCaptureFake(t, bin, test.binary, test.agent, binding, []string{"internal/provider/candidate.go"})
			environment := append(harness.environment(), fake.environment...)
			arguments := []string{"review", "capture-result", "--agent", test.agent, "--repository-context", binding["repository-context"],
				"--expected-revision", binding["expected-revision"], "--lineage", binding["lineage"], "--target", binding["target"],
				"--lens", binding["lens"], "--order", binding["order"]}
			stdout, stderr, err := runOrganicCommand(t, organicBinary, harness.repo.worktree, environment, arguments...)
			fake.assertInvoked(t)
			if err != nil {
				t.Fatalf("provider capture: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			var terminal organicFinalizeResult
			if err := json.Unmarshal([]byte(stdout), &terminal); err != nil {
				t.Fatalf("decode terminal provider capture: %v\n%s", err, stdout)
			}
			if terminal.Operation != "review/capture-result" || terminal.State != organicStateApproved {
				t.Fatalf("provider capture did not close the clean review: %#v", terminal)
			}
			harness.assertReviewAcknowledgedAndBurned(lineage, terminal)
		})
	}
}

func TestNativeProviderCaptureFailureReoffersTheSameBinding(t *testing.T) {
	for _, test := range []struct{ agent, binary string }{{"claude-code", "claude"}, {"codex", "codex"}} {
		t.Run(test.agent, func(t *testing.T) {
			harness := newOrganicHarness(t)
			harness.writeFiles(map[string]string{"internal/provider/failure.go": "package provider\n\nfunc Failure() int { return 1 }\n"})
			lineage := "provider-failure-" + test.agent
			_ = organicProviderStart(t, harness, lineage, test.agent)
			before := organicProviderBinding(t, organicProviderStatus(t, harness, lineage, test.agent))
			bin := t.TempDir()
			fake := organicWriteProviderCaptureFake(t, bin, test.binary, test.agent, nil, nil)
			environment := append(harness.environment(), fake.environment...)
			arguments := []string{"review", "capture-result", "--agent", test.agent, "--repository-context", before["repository-context"],
				"--expected-revision", before["expected-revision"], "--lineage", before["lineage"], "--target", before["target"], "--lens", before["lens"], "--order", before["order"]}
			_, _, err := runOrganicCommand(t, organicBinary, harness.repo.worktree, environment, arguments...)
			fake.assertInvoked(t)
			if err == nil {
				t.Fatal("provider transport failure unexpectedly captured a result")
			}
			after := organicProviderBinding(t, organicProviderStatus(t, harness, lineage, test.agent))
			for _, name := range []string{"lineage", "expected-revision", "target", "repository-context", "lens", "order", "subject-hash"} {
				if after[name] != before[name] {
					t.Fatalf("failure changed pending %s: before=%q after=%q", name, before[name], after[name])
				}
			}
		})
	}
}

func organicProviderStart(t *testing.T, harness *organicHarness, lineage, agent string) organicStartResult {
	t.Helper()
	status := organicProviderStatus(t, harness, lineage, agent)
	if status.NextTransition == nil || status.NextTransition.Execute == nil {
		t.Fatalf("provider START transition = %#v", status.NextTransition)
	}
	transition := status.NextTransition.Execute
	stdout, stderr, err := harness.runAllowFailure("review", "start", "--cwd", harness.repo.worktree,
		"--contract", "brunnr-code.review-integration/v2", "--target", transition.argument("target"), "--projection", transition.argument("projection"),
		"--lineage", lineage, "--agent", agent, "--consent", "granted", "--focus", "reliability")
	if err != nil {
		t.Fatalf("provider START: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var start organicStartResult
	if err := json.Unmarshal([]byte(stdout), &start); err != nil {
		t.Fatalf("decode provider START: %v\n%s", err, stdout)
	}
	return start
}

type organicProviderStatusResult struct {
	TargetIdentity string                     `json:"target_identity"`
	Applicability  string                     `json:"applicability"`
	NextTransition *organicProviderTransition `json:"next_transition"`
	Authority      struct {
		LineageID string `json:"lineage_id"`
		State     string `json:"state"`
		Revision  string `json:"revision"`
	} `json:"authority"`
	Projection struct {
		BaseTree string `json:"base_tree"`
	} `json:"projection"`
}

type organicProviderTransition struct {
	Kind       string                  `json:"kind"`
	ReasonCode string                  `json:"reason_code"`
	Execute    *organicProviderExecute `json:"execute"`
	Collect    *organicProviderCollect `json:"collect"`
}

type organicProviderExecute struct {
	Operation string                    `json:"operation"`
	Arguments []organicProviderArgument `json:"arguments"`
	Artifacts []organicProviderArtifact `json:"artifacts"`
}

type organicProviderCollect struct {
	Inputs []organicProviderCollectInput `json:"inputs"`
}

type organicProviderCollectInput struct {
	Arguments []organicProviderArgument `json:"arguments"`
}

type organicProviderArtifact struct {
	Lens              string `json:"lens"`
	SelectedOrder     int    `json:"selected_order"`
	AdmissionDecision string `json:"admission_decision"`
}

type organicProviderArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Token string `json:"token"`
}

func (execute organicProviderExecute) argument(name string) string {
	for _, argument := range execute.Arguments {
		if argument.Name == name {
			return argument.Value
		}
	}
	return ""
}

func organicProviderStatus(t *testing.T, harness *organicHarness, lineage, agent string) organicProviderStatusResult {
	t.Helper()
	payload := harness.run("review", "status", "--cwd", harness.repo.worktree, "--contract", "brunnr-code.review-integration/v2", "--agent", agent, "--lineage", lineage, "--next-transition", "--projection", "workspace")
	var status organicProviderStatusResult
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("decode provider status: %v\n%s", err, payload)
	}
	return status
}

func organicProviderBinding(t *testing.T, status organicProviderStatusResult) map[string]string {
	t.Helper()
	bindings := organicProviderBindings(t, status)
	if len(bindings) != 1 {
		t.Fatalf("provider collect bindings = %d, want one: %#v", len(bindings), status.NextTransition)
	}
	return bindings[0]
}

func organicProviderBindings(t *testing.T, status organicProviderStatusResult) []map[string]string {
	t.Helper()
	if status.NextTransition == nil || status.NextTransition.Collect == nil || len(status.NextTransition.Collect.Inputs) == 0 {
		t.Fatalf("provider collect transition = %#v", status.NextTransition)
	}
	bindings := make([]map[string]string, len(status.NextTransition.Collect.Inputs))
	for index, input := range status.NextTransition.Collect.Inputs {
		binding := make(map[string]string, len(input.Arguments))
		for _, argument := range input.Arguments {
			binding[argument.Name] = argument.Value
		}
		for _, name := range []string{"lineage", "expected-revision", "target", "repository-context", "lens", "order", "subject-hash"} {
			if binding[name] == "" {
				t.Fatalf("provider collect binding[%d] missing %q: %#v", index, name, status.NextTransition)
			}
		}
		bindings[index] = binding
	}
	return bindings
}

func TestOrganicProviderCaptureFakeWindowsDispatch(t *testing.T) {
	for _, test := range []struct {
		name, goos, binary, wantExecutable, wantPathPrefix, wantPathExt string
	}{
		{name: "Unix Claude", goos: "linux", binary: "claude", wantExecutable: "claude", wantPathPrefix: "PATH=/fake-bin:"},
		{name: "Windows Claude", goos: "windows", binary: "claude", wantExecutable: "claude.exe", wantPathPrefix: "PATH=/fake-bin;", wantPathExt: "PATHEXT=.EXE"},
		{name: "Windows Codex", goos: "windows", binary: "codex", wantExecutable: "codex.exe", wantPathPrefix: "PATH=/fake-bin;", wantPathExt: "PATHEXT=.EXE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := organicProviderCaptureFakeExecutableName(test.binary, test.goos); got != test.wantExecutable {
				t.Fatalf("fake executable = %q, want %q", got, test.wantExecutable)
			}
			environment := organicProviderCaptureFakeDispatchEnvironment("/fake-bin", test.goos)
			if !strings.HasPrefix(environment[0], test.wantPathPrefix) {
				t.Fatalf("fake PATH = %q, want prefix %q", environment[0], test.wantPathPrefix)
			}
			if got := strings.TrimPrefix(environment[0], test.wantPathPrefix); got != os.Getenv("PATH") {
				t.Fatalf("fake inherited PATH = %q, want %q", got, os.Getenv("PATH"))
			}
			if test.wantPathExt == "" {
				if len(environment) != 1 {
					t.Fatalf("Unix fake environment = %q, want only PATH", environment)
				}
				return
			}
			if len(environment) != 2 || environment[1] != test.wantPathExt {
				t.Fatalf("Windows fake environment = %q, want PATHEXT %q", environment, test.wantPathExt)
			}
		})
	}
}

type organicProviderCaptureFake struct {
	agent          string
	payload        string
	failure        bool
	invocationPath string
	environment    []string
}

type organicProviderCaptureFakeInvocation struct {
	Agent             string   `json:"agent"`
	Failure           bool     `json:"failure"`
	Payload           string   `json:"payload"`
	Arguments         []string `json:"arguments"`
	OutputLastMessage string   `json:"output_last_message"`
}

func organicWriteProviderCaptureFake(t *testing.T, directory, binary, agent string, binding map[string]string, paths []string) organicProviderCaptureFake {
	t.Helper()
	fake := organicProviderCaptureFake{
		agent:          agent,
		failure:        binding == nil,
		invocationPath: filepath.Join(directory, "provider-capture-invocation.json"),
		environment:    organicProviderCaptureFakeDispatchEnvironment(directory, runtime.GOOS),
	}
	if binding != nil {
		payload, err := json.Marshal(map[string]any{"subject_hash": binding["subject-hash"], "inspection": map[string]any{"status": "completed", "paths": paths}, "findings": []any{}, "evidence": []string{"inspected the complete frozen candidate"}})
		if err != nil {
			t.Fatal(err)
		}
		fake.payload = string(payload) + "\n"
	}
	fake.environment = append(fake.environment,
		organicProviderCaptureFakeAgentEnvironment+"="+fake.agent,
		organicProviderCaptureFakePayloadEnvironment+"="+fake.payload,
		organicProviderCaptureFakeFailureEnvironment+"="+strconv.FormatBool(fake.failure),
		organicProviderCaptureFakeInvocationEnvironment+"="+fake.invocationPath,
	)
	path := filepath.Join(directory, organicProviderCaptureFakeExecutableName(binary, runtime.GOOS))
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return fake
}

func organicProviderCaptureFakeExecutableName(binary, goos string) string {
	if goos == "windows" {
		return binary + ".exe"
	}
	return binary
}

func organicProviderCaptureFakeDispatchEnvironment(directory, goos string) []string {
	separator := ":"
	if goos == "windows" {
		separator = ";"
	}
	environment := []string{"PATH=" + directory + separator + os.Getenv("PATH")}
	if goos == "windows" {
		environment = append(environment, "PATHEXT=.EXE")
	}
	return environment
}

func (fake organicProviderCaptureFake) assertInvoked(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(fake.invocationPath)
	if err != nil {
		t.Fatalf("provider capture fake was not invoked: %v", err)
	}
	var invocation organicProviderCaptureFakeInvocation
	if err := json.Unmarshal(data, &invocation); err != nil {
		t.Fatalf("decode provider capture fake invocation: %v\n%s", err, data)
	}
	if invocation.Agent != fake.agent || invocation.Failure != fake.failure {
		t.Fatalf("provider capture fake invocation = %#v, want agent=%q failure=%t", invocation, fake.agent, fake.failure)
	}
	if !fake.failure && invocation.Payload != fake.payload {
		t.Fatalf("provider capture fake payload = %q, want %q", invocation.Payload, fake.payload)
	}
	switch fake.agent {
	case "claude-code":
		if invocation.OutputLastMessage != "" {
			t.Fatalf("Claude fake received --output-last-message=%q", invocation.OutputLastMessage)
		}
	case "codex":
		if invocation.OutputLastMessage == "" {
			t.Fatalf("Codex fake did not receive --output-last-message: %q", invocation.Arguments)
		}
	default:
		t.Fatalf("unsupported provider capture fake agent %q", fake.agent)
	}
}

// organicCorroborateRefuterPayload makes the provider capture fake answer the
// refuter request it reads on stdin with a batch corroborating every claim.
const organicCorroborateRefuterPayload = "corroborate-refuter"

func runOrganicProviderCaptureFake(agent string) int {
	prompt, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	invocation := organicProviderCaptureFakeInvocation{
		Agent:     agent,
		Failure:   os.Getenv(organicProviderCaptureFakeFailureEnvironment) == "true",
		Payload:   os.Getenv(organicProviderCaptureFakePayloadEnvironment),
		Arguments: os.Args[1:],
	}
	if invocation.Payload == organicCorroborateRefuterPayload {
		corroborated, ok := organicCorroboratingRefuterBatch(prompt)
		if !ok {
			return 1
		}
		invocation.Payload = corroborated
	}
	for index, argument := range invocation.Arguments {
		if argument == "--output-last-message" && index+1 < len(invocation.Arguments) {
			invocation.OutputLastMessage = invocation.Arguments[index+1]
			break
		}
	}
	encoded, err := json.Marshal(invocation)
	if err != nil {
		return 1
	}
	if err := os.WriteFile(os.Getenv(organicProviderCaptureFakeInvocationEnvironment), encoded, 0o600); err != nil {
		return 1
	}
	if invocation.Failure {
		return 1
	}
	if invocation.Payload == "" {
		return 1
	}
	switch agent {
	case "claude-code":
		_, err = fmt.Fprint(os.Stdout, invocation.Payload)
	case "codex":
		if invocation.OutputLastMessage == "" {
			return 1
		}
		err = os.WriteFile(invocation.OutputLastMessage, []byte(invocation.Payload), 0o600)
	default:
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

// organicRoutingGuidanceRequiredFragments pins the ODD routes, applicable
// test-first policy, and independent review guidance every configured agent
// must receive, checked by organic_runtime_real_agent_detection_test.go's
// Claude Code case.
var organicRoutingGuidanceRequiredFragments = []string{
	"Organic Driven Development",
	"Direct inline",
	"Delegated direct",
	"when a relevant runnable deterministic test and clear expected outcome exist",
	"observe RED before implementation, implement GREEN, then refactor while tests stay green",
	"For passive documentation, unavailable runners, or no meaningful runnable RED",
	"run proportionate functional or structural checks",
}

// organicRoutingGuidanceRDDFragments pins the receipt-driven development
// switch. Only RDD-capable runtimes (Claude Code, Codex) receive
// it; every other runtime is ODD-only and must not.
var organicRoutingGuidanceRDDFragments = []string{
	"brunnr-code review mode enable|disable|status",
	"disabled/unmanaged",
}

// TestOrganicReviewTierIsSelectedByEvidenceNotSize pins the proportional tier.
// Tier 0 runs zero AI reviewers and asks nothing; tier 1 runs exactly one
// consolidated review; tier 2 runs the focused 4R only when named evidence
// demands it. The two large mechanical rows exist to prove the inverse: volume
// never escalates a tier.
func TestOrganicReviewTierIsSelectedByEvidenceNotSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		files       map[string]string
		risk        string
		lensCount   int
		wantsPrompt bool
		minLines    int
	}{
		{
			name:  "tier 0 passive documentation",
			files: map[string]string{"docs/note.md": organicLines("documentation line", 12)},
			risk:  organicRiskLow,
		},
		{
			name:  "tier 0 large passive documentation",
			files: map[string]string{"docs/handbook.md": organicLines("handbook line", 2000)},
			risk:  organicRiskLow,
			// Two thousand authored lines of prose stay at zero reviewers: the
			// classifier reads content, never volume.
			minLines: 2000,
		},
		{
			name:        "tier 1 ordinary source",
			files:       map[string]string{"internal/feature/flag.go": "package feature\n\nfunc Enabled() bool { return true }\n"},
			risk:        organicRiskMedium,
			lensCount:   1,
			wantsPrompt: true,
		},
		{
			name:        "tier 1 large mechanical source",
			files:       organicMechanicalFiles(12, 100),
			risk:        organicRiskMedium,
			lensCount:   1,
			wantsPrompt: true,
			// 1200+ mechanical lines across 12 files must stay on one consolidated
			// review. Escalating here would be size-driven, not evidence-driven.
			minLines: 1200,
		},
		{
			name:        "tier 2 authorization hot path",
			files:       map[string]string{"internal/auth/session.go": "package auth\n\nfunc Session() bool { return true }\n"},
			risk:        organicRiskHigh,
			lensCount:   4,
			wantsPrompt: true,
		},
		{
			name:        "tier 2 shell process source",
			files:       map[string]string{"scripts/deploy.sh": "#!/bin/sh\nset -eu\necho deploy\n"},
			risk:        organicRiskHigh,
			lensCount:   4,
			wantsPrompt: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newOrganicHarness(t)
			harness.writeFiles(test.files)

			started, stderr := harness.startReview("organic-tier")
			if started.RiskLevel != test.risk || len(started.SelectedLenses) != test.lensCount {
				t.Fatalf("tier = %q with %d lenses, want %q with %d", started.RiskLevel, len(started.SelectedLenses), test.risk, test.lensCount)
			}
			if started.LensesRequired != (test.lensCount > 0) {
				t.Fatalf("lenses_required = %t for %d selected lenses", started.LensesRequired, test.lensCount)
			}
			if test.minLines > 0 && started.ChangedLines < test.minLines {
				t.Fatalf("changed lines = %d, want at least %d for the volume claim to mean anything", started.ChangedLines, test.minLines)
			}
			// Tier 0 is silent structural readback. Emitting a consent prompt here
			// would reintroduce exactly the ceremony the readback exists to remove.
			if prompted := strings.TrimSpace(stderr) != ""; prompted != test.wantsPrompt {
				t.Fatalf("consent prompt emitted = %t, want %t; stderr:\n%s", prompted, test.wantsPrompt, stderr)
			}

			if approved := harness.approveReview("organic-tier", started); approved.State != organicStateApproved {
				t.Fatalf("tier %q did not reach approved terminal burn: %#v", test.risk, approved)
			}
			harness.assertNoRetiredArtifacts()
		})
	}
}

// TestOrganicImplementationRoutesReachDelivery walks the two organic
// implementation routes end to end: a real actor process produces the candidate,
// proportional review reaches its terminal burn, and ordinary repository policy
// delivers the candidate under compare-and-swap.
func TestOrganicImplementationRoutesReachDelivery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		role   string
		marker string
		path   string
		body   string
	}{
		{
			name:   "direct inline",
			role:   organicActorRoleDirect,
			marker: organicDirectActorMarker,
			path:   "docs/direct-note.md",
			body:   organicLines("direct implementation line", 10),
		},
		{
			name:   "delegated direct",
			role:   organicActorRoleDelegated,
			marker: organicDelegatedActorMarker,
			path:   "docs/delegated-note.md",
			body:   organicLines("delegated implementation line", 10),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newOrganicHarness(t)
			harness.runActor(test.role, test.path, test.body, "docs: add an organic note", test.marker)

			candidate := harness.git("rev-parse", "HEAD")
			if candidate == harness.repo.baseRevision {
				t.Fatal("the actor never created a candidate commit")
			}

			lineage := "organic-" + strings.ReplaceAll(test.name, " ", "-")
			started, _ := harness.startReview(lineage, "--base-ref", "origin/main")
			approved := harness.approveReview(lineage, started)
			if approved.State != organicStateApproved {
				t.Fatalf("%s route did not approve its candidate: %#v", test.name, approved)
			}

			gate := harness.gate("pre-push")
			harness.assertInvalidatedUnmanagedGate(gate)

			harness.pushWithLease(harness.repo.baseRevision)
			harness.assertRemoteBlob(test.path, test.body)
			harness.assertOnlyMainRef()
			harness.assertStaleLeaseIsRejected(harness.repo.baseRevision)

			// The route is what this journey selects. A delegated worker in
			// particular must not promote itself.
			harness.assertNoRetiredArtifacts()
		})
	}
}

// TestOrganicDirectRouteCreatesNoRetiredArtifacts preserves the ODD negative
// control: direct work and its native review do not create retired workflow
// artifacts.
func TestOrganicDirectRouteCreatesNoRetiredArtifacts(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	harness.runActor(organicActorRoleDirect, "docs/direct.md", organicLines("direct line", 8), "docs: implement directly", organicDirectActorMarker)

	started, _ := harness.startReview("organic-direct-no-retired-artifacts", "--base-ref", "origin/main")
	if approved := harness.approveReview("organic-direct-no-retired-artifacts", started); approved.State != organicStateApproved {
		t.Fatalf("direct route did not approve: %#v", approved)
	}
	harness.assertNoRetiredArtifacts()
}

// TestOrganicBoundedCorrectionAllowsExactlyOne proves the ordinary review budget:
// one candidate-caused blocker buys one scoped correction, and the transaction
// refuses a second one instead of looping until clean.
func TestOrganicBoundedCorrectionAllowsExactlyOne(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	const lineage = "organic-correction"
	const path = "internal/feature/limit.go"

	harness.writeFiles(map[string]string{path: organicLimitSource("broken")})
	started, _ := harness.startReview(lineage)
	if started.RiskLevel != organicRiskMedium || len(started.SelectedLenses) != 1 || started.CorrectionBudget <= 0 {
		t.Fatalf("correction journey needs one consolidated review with a budget: %#v", started)
	}

	stdout, stderr, err := harness.captureReviewerResult(lineage, started, 0, organicReviewerResult{
		Lens: started.SelectedLenses[0],
		Findings: []organicFinding{{
			Location:          path + ":5",
			Severity:          "CRITICAL",
			Claim:             "the candidate returns the wrong terminal value",
			ProofRefs:         []string{"a differential test passes on base and fails on the candidate"},
			EvidenceClass:     "deterministic",
			CausalDisposition: "introduced",
		}},
		Evidence: []string{"the focused differential test failed on the candidate"},
	})
	if err != nil {
		t.Fatalf("capture candidate-caused result: %v\n%s", err, stderr)
	}
	var required organicFinalizeResult
	if err := json.Unmarshal([]byte(stdout), &required); err != nil {
		t.Fatalf("decode correction-required capture: %v\n%s", err, stdout)
	}
	if required.Operation != "review/capture-result" || required.State != organicStateCorrectionRequired {
		t.Fatalf("candidate-caused blocker did not require a correction: %#v", required)
	}

	waiting := harnessCorrectionStatus(t, harness, lineage)
	if waiting.NextTransition == nil || waiting.NextTransition.Kind != "collect" ||
		waiting.NextTransition.ReasonCode != "correction_plan_required" ||
		waiting.NextTransition.Collect == nil || len(waiting.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("correction-required review did not request a bound correction plan: %#v", waiting)
	}
	input := waiting.NextTransition.Collect.Inputs[0]
	if input.CaptureOperation != "review.capture-correction-plan" {
		t.Fatalf("correction-plan capture operation = %q", input.CaptureOperation)
	}
	planArguments := []string{"review", "capture-correction-plan"}
	hasRepositoryContext := false
	for _, argument := range input.Arguments {
		if argument.Name == "repository-context" && argument.Value != "" {
			hasRepositoryContext = true
		}
		planArguments = append(planArguments, "--"+argument.Name, argument.Value)
	}
	if !hasRepositoryContext {
		planArguments = append(planArguments, "--cwd", harness.repo.worktree)
	}
	planArguments = append(planArguments, "--correction-lines", "2")
	var forecast organicFinalizeResult
	payload := harness.run(planArguments...)
	if err := json.Unmarshal(payload, &forecast); err != nil {
		t.Fatalf("decode correction plan capture: %v\n%s", err, payload)
	}
	if forecast.Operation != "review.capture-correction-plan" || forecast.State != organicStateCorrectionRequired {
		t.Fatalf("in-budget correction plan did not retain the correction authority: %#v", forecast)
	}

	// The one forecast is immutable. Repeating it must fail before a second
	// correction route exists, rather than reviving a retired FINALIZE path.
	if _, _, err := harness.runAllowFailure(planArguments...); err == nil {
		t.Fatal("a second correction plan was accepted after the one bounded forecast")
	}

	// Materially new work starts a fresh transaction; it never reopens the
	// correction budget consumed by the burned one.
	harness.writeFiles(map[string]string{"docs/correction-follow-up.md": organicLines("fresh correction follow-up", 3)})
	fresh, _ := harness.startReview(lineage + "-fresh")
	harness.approveReview(lineage+"-fresh", fresh)
}

// initOrganicCurrentUnbornRepository creates an unborn repository for the
// negotiated target-resolution journey without reusing the tagged fixture name.
func initOrganicCurrentUnbornRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, arguments := range [][]string{
		{"init", "--quiet", "--initial-branch=main", "."},
		{"config", "user.name", "Organic E2E"},
		{"config", "user.email", "organic-e2e@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := organicGitOutput(context.Background(), repo, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// TestOrganicFlexibleDeliveryReusesOneReceipt keeps every delivery route after
// a review has approved and burned. Gates report the same non-deciding
// invalidated/unmanaged result across direct commit, direct push, and pull-request
// shapes; ordinary repository policy owns all four deliveries.
func TestOrganicFlexibleDeliveryReusesOneReceipt(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	const lineage = "organic-delivery"
	const path = "docs/delivery-note.md"
	body := organicLines("delivery line", 10)

	harness.writeFiles(map[string]string{path: body})
	harness.git("add", "--", path)

	started, _ := harness.startReview(lineage, "--projection", "staged")
	if approved := harness.approveReview(lineage, started); approved.State != organicStateApproved {
		t.Fatalf("staged candidate did not approve: %#v", approved)
	}

	// Route 1: direct commit.
	commitGate := harness.gate("pre-commit")
	harness.assertInvalidatedUnmanagedGate(commitGate)
	harness.git("commit", "-q", "-m", "docs: add a delivery note")

	// Route 2: direct push after the commit.
	pushGate := harness.gate("pre-push")
	harness.assertInvalidatedUnmanagedGate(pushGate)

	// Routes 3 and 4: pull requests with and without an issue reference remain
	// ordinary repository policy. The informational gate must not reintroduce a
	// receipt or change behavior because the commit message changed.
	prGate := harness.gate("pre-pr", "--base-ref", "origin/main")
	harness.assertInvalidatedUnmanagedGate(prGate)
	harness.git("checkout", "-q", "-b", "organic-pr-with-issue")
	harness.git("commit", "-q", "--amend", "--allow-empty", "-m", "docs: add a delivery note\n\nRefs: #17")
	issueGate := harness.gate("pre-pr", "--base-ref", "origin/main")
	harness.assertInvalidatedUnmanagedGate(issueGate)

	harness.git("checkout", "-q", "main")
	harness.pushWithLease(harness.repo.baseRevision)
	harness.assertRemoteBlob(path, body)
	harness.assertNoRetiredArtifacts()
	harness.assertOnlyMainRef()
}

// TestOrganicKillSwitchStopsAtTheDeliveryBoundary proves safe disablement. The
// candidate still exists locally, nothing reaches the remote, no authority
// generation is written, and the refusal is typed and discoverable rather than a
// silent no-op or a fabricated approval.
func TestOrganicKillSwitchStopsAtTheDeliveryBoundary(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	harness.runActor(organicActorRoleDirect, "docs/killed.md", organicLines("killed line", 8), "docs: implement before the switch", organicDirectActorMarker)

	mode := harness.disableReview()
	if mode.Schema != organicModeSchema || mode.Status.Effective != organicModeOff || mode.Status.Source != "clone_local" {
		t.Fatalf("kill switch produced no typed outcome: %#v", mode)
	}
	generationsAfterDisable := harness.reviewModeGenerations()

	// The candidate is committed locally. That is deliberate: disabling review
	// must never destroy the user's work.
	if harness.git("rev-parse", "HEAD") == harness.repo.baseRevision {
		t.Fatal("the kill-switch journey never reached a committed candidate")
	}

	// The universal empty-candidate guard (issue #2586) refuses a clean tree
	// before the kill switch can name itself, so the disabled attempt carries
	// a real pending candidate: the refusal under test here is the kill
	// switch's, and it must name its deciding source.
	harness.writeFiles(map[string]string{"docs/disabled-attempt.md": organicLines("pending while disabled", 3)})
	harness.git("add", "--", "docs/disabled-attempt.md")
	_, stderr, err := harness.runAllowFailure("review", "start", "--cwd", harness.repo.worktree, "--lineage", "organic-killed")
	if err == nil {
		t.Fatal("review start succeeded while receipt-driven development was disabled")
	}
	if !strings.Contains(stderr, "receipt-driven development is disabled") || !strings.Contains(stderr, "clone_local") {
		t.Fatalf("disabled start was refused without naming the deciding source: %s", stderr)
	}
	harness.git("rm", "-f", "-q", "--", "docs/disabled-attempt.md")

	// The delivery boundary reports an unmanaged, receiptless candidate instead of
	// inventing an approval.
	gate := harness.gateAllowFailure("pre-push")
	if gate.Schema != organicGateSchema || gate.Allowed || gate.Result == organicGateAllow {
		t.Fatalf("disabled delivery gate did not fail closed: %#v", gate)
	}
	// Wave 5 Slice 2 (design decision 4): the kill switch is consulted
	// before any authority read, so this report carries no discovery-kind
	// detail at all -- there is no receipt-discovery outcome to describe,
	// because discovery never runs.
	if gate.Context.Denial != nil {
		t.Fatalf("disabled delivery gate leaked discovery-kind detail: %#v", gate.Context.Denial)
	}
	// The guidance installed on all 16 adapters promises this exact token under a
	// disabled switch. Asserting it here is what keeps that promise honest, and
	// distinguishes "unmanaged by choice" from "blocked because something broke".
	if gate.Delivery != "disabled/unmanaged" {
		t.Fatalf("disabled delivery gate did not report the promised disposition: %q", gate.Delivery)
	}

	// Zero effects: no review authority, no additional compare-and-swap
	// generation, and a remote that never moved.
	if _, err := os.Stat(filepath.Join(harness.commonDir(), "brunnr-code", "review-transactions", "v2")); !os.IsNotExist(err) {
		t.Fatalf("a disabled start still created review authority: %v", err)
	}
	if after := harness.reviewModeGenerations(); !equalOrganicStrings(after, generationsAfterDisable) {
		t.Fatalf("a disabled start advanced review-mode CAS generations: %v -> %v", generationsAfterDisable, after)
	}
	remote := harness.bareGit("rev-parse", "refs/heads/main")
	if remote != harness.repo.baseRevision {
		t.Fatalf("the remote moved while review was disabled: %s != %s", remote, harness.repo.baseRevision)
	}
	harness.assertOnlyMainRef()
	harness.assertNoRetiredArtifacts()
}

// TestOrganicKillSwitchReportsUnmanagedDeliveryOverPriorReceipts proves the
// disabled disposition survives review history (community report, PR #1801).
// The virgin-clone journey above holds without any receipts; this one holds in
// a repository that already completed reviewed flows: a stale receipt that no
// longer governs the candidate is the expected state of a disabled clone — no
// new receipt could have been created while disabled — so the gate still
// reports `disabled/unmanaged` instead of failing closed on a receipt mismatch.
func TestOrganicKillSwitchReportsUnmanagedDeliveryOverPriorReceipts(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	const lineage = "organic-killed-prior"
	const path = "docs/prior-note.md"

	// A completed reviewed flow: the candidate is committed and reviewed
	// against its base, so a terminal receipt exists before the switch flips.
	harness.writeFiles(map[string]string{path: organicLines("prior line", 8)})
	harness.git("add", "--", path)
	harness.git("commit", "-q", "-m", "docs: reviewed before the switch")
	started, _ := harness.startReview(lineage, "--base-ref", harness.repo.baseRevision, "--committed-only")
	if approved := harness.approveReview(lineage, started); approved.State != organicStateApproved {
		t.Fatalf("the prior reviewed flow did not approve its candidate: %#v", approved)
	}

	if mode := harness.disableReview(); mode.Status.Effective != organicModeOff {
		t.Fatalf("kill switch produced no typed outcome: %#v", mode)
	}

	// The community-reported shape: a new commit authored while disabled, in a
	// repository that still holds the earlier receipt.
	harness.writeFiles(map[string]string{"docs/disabled-note.md": organicLines("disabled line", 6)})
	harness.git("add", "--", "docs/disabled-note.md")
	harness.git("commit", "-q", "-m", "docs: authored while disabled")

	// harness.gate fails the test on a non-zero exit, so this also proves the
	// gate reports instead of vetoing.
	gate := harness.gate("pre-push")
	if gate.Schema != organicGateSchema || gate.Allowed || gate.Result == organicGateAllow {
		t.Fatalf("disabled delivery over a prior receipt fabricated an approval: %#v", gate)
	}
	if gate.Delivery != "disabled/unmanaged" {
		t.Fatalf("disabled delivery over a prior receipt did not report the promised disposition: %q", gate.Delivery)
	}
	// Wave 5 Slice 2 (design decision 4): the switch is consulted before any
	// authority read, so the prior receipt is never even discovered while
	// disabled -- no discovery-kind detail leaks.
	if gate.Context.Denial != nil {
		t.Fatalf("disabled delivery over a prior receipt leaked discovery-kind detail: %#v", gate.Context.Denial)
	}

	// Reporting moved nothing: the remote is untouched and no branch appeared.
	if remote := harness.bareGit("rev-parse", "refs/heads/main"); remote != harness.repo.baseRevision {
		t.Fatalf("the remote moved while review was disabled: %s != %s", remote, harness.repo.baseRevision)
	}
	harness.assertOnlyMainRef()
	harness.assertNoRetiredArtifacts()
}

// TestOrganicKillSwitchReportsUnmanagedDeliveryOverWorkspaceReceipt proves the
// second community-reported shape (Wladimirfn, PR #1801): a workspace
// (current-changes) receipt delivered exactly as reviewed in one commit, then a
// new commit authored while disabled, then pre-push. The candidate now
// publishes two commits past the reviewed base, so the receipt's one-commit
// delivery rule cannot hold — a deterministic mismatch between candidate shape
// and a provably healthy receipt, never corruption — and the disabled gate
// still reports `disabled/unmanaged` with a successful exit.
func TestOrganicKillSwitchReportsUnmanagedDeliveryOverWorkspaceReceipt(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	const lineage = "organic-killed-workspace"
	const path = "docs/workspace-note.md"

	// A completed workspace reviewed flow over the dirty candidate...
	harness.writeFiles(map[string]string{path: organicLines("workspace line", 8)})
	started, _ := harness.startReview(lineage)
	if approved := harness.approveReview(lineage, started); approved.State != organicStateApproved {
		t.Fatalf("the workspace reviewed flow did not approve its candidate: %#v", approved)
	}
	// ...delivered exactly as reviewed, in one commit that was never pushed.
	harness.git("add", "--", path)
	harness.git("commit", "-q", "-m", "docs: reviewed workspace delivery")

	if mode := harness.disableReview(); mode.Status.Effective != organicModeOff {
		t.Fatalf("kill switch produced no typed outcome: %#v", mode)
	}

	// The community-reported shape: a second commit authored while disabled.
	harness.writeFiles(map[string]string{"docs/disabled-note.md": organicLines("disabled line", 6)})
	harness.git("add", "--", "docs/disabled-note.md")
	harness.git("commit", "-q", "-m", "docs: authored while disabled")

	// harness.gate fails the test on a non-zero exit, so this also proves the
	// gate reports instead of vetoing with a fabricated corruption verdict.
	gate := harness.gate("pre-push")
	if gate.Schema != organicGateSchema || gate.Allowed || gate.Result == organicGateAllow {
		t.Fatalf("disabled delivery over a workspace receipt fabricated an approval: %#v", gate)
	}
	if gate.Delivery != "disabled/unmanaged" {
		t.Fatalf("disabled delivery over a workspace receipt did not report the promised disposition: %q", gate.Delivery)
	}
	// Wave 5 Slice 2 (design decision 4): the switch is consulted before any
	// authority read, so the healthy receipt is never even discovered while
	// disabled -- no discovery-kind detail (not even "delivery-shape-mismatch")
	// leaks.
	if gate.Context.Denial != nil {
		t.Fatalf("disabled delivery over a workspace receipt leaked discovery-kind detail: %#v", gate.Context.Denial)
	}

	// Reporting moved nothing: the remote is untouched and no branch appeared.
	if remote := harness.bareGit("rev-parse", "refs/heads/main"); remote != harness.repo.baseRevision {
		t.Fatalf("the remote moved while review was disabled: %s != %s", remote, harness.repo.baseRevision)
	}
	harness.assertOnlyMainRef()
	harness.assertNoRetiredArtifacts()
}

// TestOrganicKillSwitchReEnableLandsOnTheFreshFullReview drives issue #1877's
// sequence through the terminal-burn model. Review mode changes only the
// informational delivery disposition; it does not restore burned authority.
func TestOrganicKillSwitchReEnableLandsOnTheFreshFullReview(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	harness.writeFiles(map[string]string{"docs/baseline.md": organicLines("baseline line", 6)})
	harness.git("add", "--", "docs/baseline.md")
	baselineStarted, _ := harness.startReview("organic-reenable-baseline")
	if approved := harness.approveReview("organic-reenable-baseline", baselineStarted); approved.State != organicStateApproved {
		t.Fatalf("the baseline reviewed flow did not approve: %#v", approved)
	}
	harness.assertInvalidatedUnmanagedGate(harness.gate("pre-commit"))
	harness.git("commit", "-q", "-m", "docs: baseline reviewed delivery")

	if mode := harness.disableReview(); mode.Status.Effective != organicModeOff {
		t.Fatalf("kill switch produced no typed outcome: %#v", mode)
	}
	for _, unit := range []string{"one", "two"} {
		harness.writeFiles(map[string]string{"docs/unmanaged-" + unit + ".md": organicLines("unmanaged "+unit, 6)})
		harness.git("add", "--", "docs/unmanaged-"+unit+".md")
		gate := harness.gate("pre-commit")
		if gate.Allowed || gate.Result == organicGateAllow || gate.Delivery != "disabled/unmanaged" {
			t.Fatalf("disabled delivery gate for unit %s = %#v, want disabled/unmanaged", unit, gate)
		}
		harness.git("commit", "-q", "-m", "docs: unmanaged delivery "+unit)
	}

	if mode := harness.enableReview(); mode.Status.Effective != organicModeOn {
		t.Fatalf("re-enable produced no typed outcome: %#v", mode)
	}

	// New work after re-enable remains ordinary delivery until an independently
	// started review is requested; validate is informational and cannot decide it.
	harness.writeFiles(map[string]string{"docs/next.md": organicLines("next line", 6)})
	harness.git("add", "--", "docs/next.md")
	harness.assertInvalidatedUnmanagedGate(harness.gate("pre-commit"))
	harness.git("commit", "-q", "-m", "docs: managed unit after re-enable")
}

// TestOrganicTerminalAuthoritySurvivesWithdrawalAndReplaysWithoutEffect keeps
// the withdrawal journey at the terminal-burn boundary. FINALIZE cannot be
// replayed because it leaves no authority; disabling and re-enabling changes only
// the informational gate disposition, and later work begins a fresh transaction.
func TestOrganicTerminalAuthoritySurvivesWithdrawalAndReplaysWithoutEffect(t *testing.T) {
	t.Parallel()
	harness := newOrganicHarness(t)
	const lineage = "organic-withdrawal"

	harness.writeFiles(map[string]string{"docs/withdrawn.md": organicLines("withdrawal line", 10)})
	started, _ := harness.startReview(lineage)
	if approved := harness.approveReview(lineage, started); approved.State != organicStateApproved {
		t.Fatalf("withdrawal journey did not approve its candidate: %#v", approved)
	}
	harness.assertInvalidatedUnmanagedGate(harness.gate("post-apply"))

	if _, _, err := harness.runAllowFailure("review", "finalize", "--cwd", harness.repo.worktree, "--lineage", lineage); err == nil {
		t.Fatal("terminal finalize replay reused burned authority")
	}
	if _, err := os.Stat(filepath.Join(harness.commonDir(), "brunnr-code", "review-transactions", "v2", lineage)); !os.IsNotExist(err) {
		t.Fatalf("terminal replay recreated authority: %v", err)
	}

	if mode := harness.disableReview(); mode.Status.Effective != organicModeOff {
		t.Fatalf("authorization withdrawal did not take effect: %#v", mode)
	}
	harness.writeFiles(map[string]string{"docs/withdrawn-successor.md": organicLines("withdrawn successor", 4)})
	harness.git("add", "--", "docs/withdrawn-successor.md")
	if _, _, err := harness.runAllowFailure("review", "start", "--cwd", harness.repo.worktree, "--lineage", "organic-withdrawal-successor"); err == nil {
		t.Fatal("a fresh review started after the authorization was withdrawn")
	}
	withdrawn := harness.gate("post-apply")
	if withdrawn.Allowed || withdrawn.Result == organicGateAllow || withdrawn.Delivery != "disabled/unmanaged" {
		t.Fatalf("withdrawn delivery gate = %#v, want disabled/unmanaged", withdrawn)
	}

	if mode := harness.enableReview(); mode.Status.Effective != organicModeOn {
		t.Fatalf("re-enabling did not take effect: %#v", mode)
	}
	harness.assertInvalidatedUnmanagedGate(harness.gate("post-apply"))
	fresh, _ := harness.startReview("organic-withdrawal-fresh")
	harness.approveReview("organic-withdrawal-fresh", fresh)
	harness.assertOnlyMainRef()
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type organicRepository struct {
	worktree     string
	bare         string
	baseRevision string
}

type organicHarness struct {
	t    *testing.T
	repo organicRepository
	home string
}

// newOrganicHarness builds the shared fixture every review-lifecycle journey
// runs against: a seeded repository, an isolated HOME, and an explicit global
// opt-in into receipt-driven development.
//
// Explicit enable is part of the fixture so lifecycle journeys do not depend
// on the ON default. The real command runs against this fixture's isolated HOME
// and gives kill-switch journeys a persisted ON opinion to switch off.
func newOrganicHarness(t *testing.T) *organicHarness {
	t.Helper()
	harness := &organicHarness{t: t, repo: initOrganicRepository(t), home: t.TempDir()}
	harness.enableReviewGlobally()
	return harness
}

// newOrganicHarnessForWorktree wraps an already-initialized worktree in the
// same fixture. Journeys that need a repository shape initOrganicRepository
// cannot produce (an unborn HEAD, for instance) build the worktree themselves,
// but they still need the isolated HOME and the same global opt-in.
func newOrganicHarnessForWorktree(t *testing.T, worktree string) *organicHarness {
	t.Helper()
	harness := &organicHarness{t: t, repo: organicRepository{worktree: worktree}, home: t.TempDir()}
	harness.enableReviewGlobally()
	return harness
}

// environment isolates the run from the developer's own global review mode. A
// suite that reads the real user state would pass or fail for reasons that have
// nothing to do with the product.
func (harness *organicHarness) environment() []string {
	return organicEnvironment(harness.home)
}

func organicEnvironment(home string) []string {
	environment := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		// CI makes the one-time consent question deterministically unanswerable,
		// which is exactly the non-interactive path this suite asserts on.
		"CI=1",
	}
	if value := os.Getenv("SYSTEMROOT"); value != "" {
		environment = append(environment, "SYSTEMROOT="+value)
	}
	if value := os.Getenv("TMPDIR"); value != "" {
		environment = append(environment, "TMPDIR="+value)
	}
	for _, name := range []string{"OPENCODE_DISABLE_PROJECT_CONFIG", "OPENCODE_DISABLE_EXTERNAL_SKILLS"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func (harness *organicHarness) run(arguments ...string) []byte {
	harness.t.Helper()
	stdout, stderr, err := runOrganicCommand(harness.t, organicBinary, harness.repo.worktree, harness.environment(), arguments...)
	if err != nil {
		harness.t.Fatalf("brunnr-code %v: %v\nstdout:\n%s\nstderr:\n%s", arguments, err, stdout, stderr)
	}
	return []byte(stdout)
}

func (harness *organicHarness) runAllowFailure(arguments ...string) (string, string, error) {
	harness.t.Helper()
	return runOrganicCommand(harness.t, organicBinary, harness.repo.worktree, harness.environment(), arguments...)
}

func runOrganicCommand(t *testing.T, binary, dir string, environment []string, arguments ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(ctx, binary, arguments...)
	command.Dir = dir
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

// startReview freezes the live candidate and returns both the typed result and
// the console stream, because whether a question was asked is itself an
// assertion in the tier-0 journey.
func (harness *organicHarness) startReview(lineage string, extra ...string) (organicStartResult, string) {
	harness.t.Helper()
	arguments := []string{"review", "start", "--cwd", harness.repo.worktree}
	if lineage != "" {
		arguments = append(arguments, "--lineage", lineage)
	}
	arguments = append(arguments, extra...)
	stdout, stderr, err := harness.runAllowFailure(arguments...)
	if err != nil {
		harness.t.Fatalf("review start %v: %v\nstdout:\n%s\nstderr:\n%s", arguments, err, stdout, stderr)
	}
	var started organicStartResult
	if err := json.Unmarshal([]byte(stdout), &started); err != nil {
		harness.t.Fatalf("decode review start: %v\n%s", err, stdout)
	}
	started.statusSelectors = organicStatusSelectorsFromStartArguments(extra)
	return started, stderr
}

func organicStatusSelectorsFromStartArguments(arguments []string) []string {
	selectors := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--base-ref", "--projection":
			if index+1 < len(arguments) {
				selectors = append(selectors, arguments[index], arguments[index+1])
				index++
			}
		case "--committed-only", "--workspace-overlay":
			selectors = append(selectors, arguments[index])
		}
	}
	return selectors
}

// approveReview runs the proportional plan the tier selected. The final
// admitted reviewer event leaves approved authority pending until the exact
// v2 acknowledgement from the closure and a restarted STATUS burns it.
func (harness *organicHarness) approveReview(lineage string, started organicStartResult) organicFinalizeResult {
	harness.t.Helper()
	if len(started.SelectedLenses) == 0 {
		finalized := organicFinalizeResult{
			LineageID: lineage, State: started.State, Action: started.Action, Acknowledgement: started.Acknowledgement,
			statusSelectors: organicAcknowledgementStatusSelectors(started),
		}
		harness.assertReviewAcknowledgedAndBurned(lineage, finalized)
		return finalized
	}
	var finalized organicFinalizeResult
	for index, lens := range started.SelectedLenses {
		stdout, stderr, err := harness.captureReviewerResult(lineage, started, index, organicReviewerResult{
			Lens:     lens,
			Findings: []organicFinding{},
			Evidence: []string{"inspected every frozen candidate path for " + lens},
		})
		if err != nil {
			harness.t.Fatalf("capture reviewer result for %s: %v\n%s", lens, err, stderr)
		}
		if index == len(started.SelectedLenses)-1 {
			if err := json.Unmarshal([]byte(stdout), &finalized); err != nil {
				harness.t.Fatalf("decode terminal reviewer capture: %v\n%s", err, stdout)
			}
		}
	}
	finalized.statusSelectors = organicAcknowledgementStatusSelectors(started)
	harness.assertReviewAcknowledgedAndBurned(lineage, finalized)
	return finalized
}

func organicAcknowledgementStatusSelectors(started organicStartResult) []string {
	if len(started.statusSelectors) > 0 {
		return append([]string(nil), started.statusSelectors...)
	}
	switch started.TargetMode {
	case "base-diff":
		if started.BaseTree != "" {
			return []string{"--base-ref", started.BaseTree, "--committed-only"}
		}
	case "base-workspace-overlay":
		if started.BaseTree != "" {
			selectors := []string{"--base-ref", started.BaseTree, "--workspace-overlay"}
			if started.Projection == "staged" {
				selectors = append(selectors, "--projection", "staged")
			}
			return selectors
		}
	case "current-changes":
		if started.Projection == "staged" {
			return []string{"--projection", "staged"}
		}
	}
	return nil
}

func organicApprovedAcknowledgementStatus(t *testing.T, harness *organicHarness, lineage string, selectors ...string) *organicProviderExecute {
	t.Helper()
	arguments := []string{
		"review", "status", "--cwd", harness.repo.worktree, "--contract", "brunnr-code.review-integration/v2",
		"--lineage", lineage, "--next-transition",
	}
	arguments = append(arguments, selectors...)
	payload := harness.run(arguments...)
	var status organicProviderStatusResult
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("decode approved acknowledgement STATUS: %v\n%s", err, payload)
	}
	if status.Authority.LineageID != lineage || status.Authority.State != organicStateApproved || status.Authority.Revision == "" ||
		status.NextTransition == nil || status.NextTransition.Kind != "execute" ||
		status.NextTransition.ReasonCode != "approved_acknowledgement_required" || status.NextTransition.Execute == nil {
		t.Fatalf("approved acknowledgement STATUS = %#v", status)
	}
	return status.NextTransition.Execute
}

func organicAcknowledgementArguments(t *testing.T, execution *organicProviderExecute, repo, lineage string) []string {
	t.Helper()
	if execution == nil || execution.Operation != "review.acknowledge-approved" || len(execution.Arguments) != 5 {
		t.Fatalf("acknowledgement execution = %#v, want exact v2 acknowledgement", execution)
	}
	wantNames := []string{"cwd", "lineage", "target", "expected-revision", "token"}
	tokens := make([]string, len(wantNames))
	for index, name := range wantNames {
		argument := execution.Arguments[index]
		if argument.Name != name || argument.Value == "" || argument.Token == "" {
			t.Fatalf("acknowledgement argument %d = %#v, want named non-empty %q argument", index, argument, name)
		}
		tokens[index] = argument.Token
	}
	// Compared by directory identity, not by string: on Windows the harness
	// holds the 8.3 short TEMP form while Go emits the canonical worktree root.
	if !sameOrganicDirectory(execution.Arguments[0].Value, repo) || execution.Arguments[1].Value != lineage ||
		len(execution.Arguments[4].Value) != 64 || !strings.HasPrefix(execution.Arguments[4].Token, "--token=") {
		t.Fatalf("acknowledgement binding = %#v, want exact repo/lineage and canonical token", execution)
	}
	return tokens
}

func assertSameOrganicAcknowledgement(t *testing.T, want, got *organicProviderExecute) {
	t.Helper()
	if want == nil || got == nil || want.Operation != got.Operation || len(want.Arguments) != len(got.Arguments) {
		t.Fatalf("acknowledgement restart = %#v, want %#v", got, want)
	}
	for index := range want.Arguments {
		if want.Arguments[index] != got.Arguments[index] {
			t.Fatalf("acknowledgement restart argument %d = %#v, want %#v", index, got.Arguments[index], want.Arguments[index])
		}
	}
}

// assertReviewAcknowledgedAndBurned pins the terminal ownership boundary. The
// closure emits one pending acknowledgement; restarted STATUS replays the exact
// operation, token, and live revision. Wrong and replayed invocations refuse;
// only the exact invocation removes authority without a receipt or sidecar.
func (harness *organicHarness) assertReviewAcknowledgedAndBurned(lineage string, finalized organicFinalizeResult) {
	harness.t.Helper()
	if finalized.State != organicStateApproved {
		harness.t.Fatalf("review %q finalized as %q, want %q: %#v", lineage, finalized.State, organicStateApproved, finalized)
	}
	if finalized.ReceiptPath != "" {
		harness.t.Fatalf("approved review %q retained receipt path %q", lineage, finalized.ReceiptPath)
	}
	tokens := organicAcknowledgementArguments(harness.t, finalized.Acknowledgement, harness.repo.worktree, lineage)
	restarted := organicApprovedAcknowledgementStatus(harness.t, harness, lineage, finalized.statusSelectors...)
	organicAcknowledgementArguments(harness.t, restarted, harness.repo.worktree, lineage)
	assertSameOrganicAcknowledgement(harness.t, finalized.Acknowledgement, restarted)

	wrong := append([]string{"review", "acknowledge-approved"}, tokens...)
	wrongToken := strings.Repeat("0", 64)
	if wrongToken == finalized.Acknowledgement.Arguments[4].Value {
		wrongToken = strings.Repeat("1", 64)
	}
	wrong[len(wrong)-1] = "--token=" + wrongToken
	if _, _, err := harness.runAllowFailure(wrong...); err == nil {
		harness.t.Fatal("wrong acknowledgement binding burned authority")
	}
	afterWrong := organicApprovedAcknowledgementStatus(harness.t, harness, lineage, finalized.statusSelectors...)
	assertSameOrganicAcknowledgement(harness.t, finalized.Acknowledgement, afterWrong)

	exact := append([]string{"review", "acknowledge-approved"}, tokens...)
	if _, stderr, err := harness.runAllowFailure(exact...); err != nil {
		harness.t.Fatalf("exact acknowledgement failed: %v\n%s", err, stderr)
	}
	if _, _, err := harness.runAllowFailure(exact...); err == nil {
		harness.t.Fatal("replayed acknowledgement recreated or burned authority")
	}
	authority := filepath.Join(harness.commonDir(), "brunnr-code", "review-transactions", "v2", lineage)
	if _, err := os.Stat(authority); !os.IsNotExist(err) {
		harness.t.Fatalf("approved review %q retained authority after acknowledgement at %q: %v", lineage, authority, err)
	}
}

// assertInvalidatedUnmanagedGate keeps delivery on ordinary repository policy
// after an approved transaction has burned. A gate may report that fact, but it
// must neither authorize nor veto delivery.
func (harness *organicHarness) assertInvalidatedUnmanagedGate(gate organicGateResult) {
	harness.t.Helper()
	if gate.Schema != organicGateSchema || gate.Allowed || gate.Result != "invalidated" || gate.Delivery != "unmanaged" {
		harness.t.Fatalf("delivery gate = %#v, want invalidated/unmanaged informational result", gate)
	}
	if gate.Action != "repository-policy" {
		harness.t.Fatalf("delivery gate action = %q, want repository-policy", gate.Action)
	}
}

// captureReviewerResult admits one reviewer result through the native route.
//
// finalize takes no unadmitted reviewer results, so the suite asks the binary
// for the lens's frozen binding, echoes the provider-issued subject hash and the
// inspected path manifest back into the caller's payload, and captures it. Only
// the binding is supplied here; the findings and evidence stay exactly as the
// caller wrote them, so a test that means to submit a rejectable result still
// submits one.
func (harness *organicHarness) captureReviewerResult(lineage string, started organicStartResult, order int, result organicReviewerResult) (string, string, error) {
	harness.t.Helper()
	lens := started.SelectedLenses[order]
	binding := []string{
		"review", "capture-result", "--cwd", harness.repo.worktree, "--lineage", lineage,
		"--target", started.targetIdentity(), "--lens", lens, "--order", strconv.Itoa(order),
	}
	var preflight organicCapturePreflight
	if err := json.Unmarshal(harness.run(append(binding, "--preflight")...), &preflight); err != nil {
		harness.t.Fatalf("decode capture-result preflight for %s: %v", lens, err)
	}
	paths := make([]string, len(preflight.ChangedPathManifest))
	for index, entry := range preflight.ChangedPathManifest {
		paths[index] = entry.Path
	}
	result.SubjectHash = preflight.ArtifactSubject.SubjectHash
	result.Inspection = &organicInspection{Status: "completed", Paths: paths}
	input := harness.writeJSON(fmt.Sprintf("reviewer-%d.json", order), result)
	return harness.runAllowFailure(append(binding, "--input", input)...)
}

// corroborateRefuter submits the one refuter batch a severe candidate-caused
// finding requires once every lens is captured (L20: deterministic findings
// reach the refuter too). It follows only public routes: the provider STATUS
// issues the refuter binding and the compiled claude-code capture runs a fake
// provider that corroborates every claim of the exact Go-issued request. It
// returns the raw terminal closure the refuter capture prints.
func (harness *organicHarness) corroborateRefuter(lineage string) string {
	harness.t.Helper()
	status := organicProviderStatus(harness.t, harness, lineage, "claude-code")
	if status.NextTransition == nil || status.NextTransition.ReasonCode != "provider_refuter_required" ||
		status.NextTransition.Collect == nil || len(status.NextTransition.Collect.Inputs) != 1 {
		harness.t.Fatalf("refuter STATUS = %#v, want one provider_refuter_required input", status.NextTransition)
	}
	binding := []string{"review", "capture-refuter", "--cwd", harness.repo.worktree, "--agent", "claude-code", "--execute=true"}
	arguments := map[string]string{}
	for _, argument := range status.NextTransition.Collect.Inputs[0].Arguments {
		arguments[argument.Name] = argument.Value
	}
	for _, name := range []string{"repository-context", "lineage", "target", "expected-revision"} {
		if arguments[name] == "" {
			harness.t.Fatalf("refuter binding lacks %q: %#v", name, status.NextTransition)
		}
		binding = append(binding, "--"+name, arguments[name])
	}
	bin := harness.t.TempDir()
	fake := organicWriteProviderCaptureFake(harness.t, bin, "claude", "claude-code", map[string]string{}, nil)
	environment := append(harness.environment(), fake.environment...)
	for index, entry := range environment {
		if strings.HasPrefix(entry, organicProviderCaptureFakePayloadEnvironment+"=") {
			environment[index] = organicProviderCaptureFakePayloadEnvironment + "=" + organicCorroborateRefuterPayload
		}
	}
	stdout, stderr, err := runOrganicCommand(harness.t, organicBinary, harness.repo.worktree, environment, binding...)
	if err != nil {
		harness.t.Fatalf("brunnr-code %v: %v\nstdout:\n%s\nstderr:\n%s", binding, err, stdout, stderr)
	}
	return stdout
}

// organicCorroboratingRefuterBatch answers the refuter request embedded in a
// Go-issued role prompt with a batch corroborating every claim.
func organicCorroboratingRefuterBatch(prompt []byte) (string, bool) {
	_, input, found := bytes.Cut(prompt, []byte("\n\nInput:\n"))
	if found {
		input, _, found = bytes.Cut(input, []byte("\n\nOutput schema:\n"))
	}
	var request struct {
		RequestHash string `json:"request_hash"`
		Claims      []struct {
			FindingID string `json:"finding_id"`
		} `json:"claims"`
	}
	if !found || json.Unmarshal(input, &request) != nil || request.RequestHash == "" || len(request.Claims) == 0 {
		return "", false
	}
	type outcome struct {
		FindingID string   `json:"finding_id"`
		Outcome   string   `json:"outcome"`
		ProofRefs []string `json:"proof_refs"`
	}
	results := make([]outcome, 0, len(request.Claims))
	for _, claim := range request.Claims {
		results = append(results, outcome{FindingID: claim.FindingID, Outcome: "corroborated", ProofRefs: []string{"independent reproduction of " + claim.FindingID}})
	}
	encoded, err := json.Marshal(struct {
		RequestHash string    `json:"refuter_request_hash"`
		Results     []outcome `json:"results"`
	}{request.RequestHash, results})
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// captureReviewerResultOrFail is captureReviewerResult for the callers that
// require admission to succeed.
func (harness *organicHarness) captureReviewerResultOrFail(lineage string, started organicStartResult, order int, result organicReviewerResult) {
	harness.t.Helper()
	if _, stderr, err := harness.captureReviewerResult(lineage, started, order, result); err != nil {
		harness.t.Fatalf("capture reviewer result for %s: %v\n%s", started.SelectedLenses[order], err, stderr)
	}
}

type organicCapturePreflight struct {
	ArtifactSubject struct {
		SubjectHash string `json:"subject_hash"`
	} `json:"artifact_subject"`
	ChangedPathManifest []struct {
		Path string `json:"path"`
	} `json:"changed_path_manifest"`
}

type organicInspection struct {
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
}

func (harness *organicHarness) gate(gate string, extra ...string) organicGateResult {
	harness.t.Helper()
	arguments := append([]string{"review", "validate", "--cwd", harness.repo.worktree, "--gate", gate}, extra...)
	var result organicGateResult
	payload := harness.run(arguments...)
	if err := json.Unmarshal(payload, &result); err != nil {
		harness.t.Fatalf("decode review validate: %v\n%s", err, payload)
	}
	return result
}

// gateAllowFailure decodes a denied gate. A denial exits non-zero on purpose, so
// the typed projection still has to be readable.
func (harness *organicHarness) gateAllowFailure(gate string, extra ...string) organicGateResult {
	harness.t.Helper()
	arguments := append([]string{"review", "validate", "--cwd", harness.repo.worktree, "--gate", gate}, extra...)
	stdout, _, _ := harness.runAllowFailure(arguments...)
	var result organicGateResult
	decoder := json.NewDecoder(strings.NewReader(stdout))
	if err := decoder.Decode(&result); err != nil {
		harness.t.Fatalf("decode denied review validate: %v\n%s", err, stdout)
	}
	return result
}

func (harness *organicHarness) disableReview() organicModeResult {
	harness.t.Helper()
	payload := harness.run("review", "mode", "disable", "--cwd", harness.repo.worktree, "--scope", "clone", "--json")
	var mode organicModeResult
	if err := json.Unmarshal(payload, &mode); err != nil {
		harness.t.Fatalf("decode review mode: %v\n%s", err, payload)
	}
	return mode
}

// enableReviewGlobally records the user-scoped `on` this fixture's isolated
// HOME declares as a lifecycle precondition, independently of default ON.
// Only global enable persists `on`; clone enable merely clears a local OFF.
func (harness *organicHarness) enableReviewGlobally() organicModeResult {
	harness.t.Helper()
	payload := harness.run("review", "mode", "enable", "--cwd", harness.repo.worktree, "--scope", "global", "--json")
	var mode organicModeResult
	if err := json.Unmarshal(payload, &mode); err != nil {
		harness.t.Fatalf("decode review mode: %v\n%s", err, payload)
	}
	if mode.Status.Effective != organicModeOn {
		harness.t.Fatalf("global opt-in left review off: %#v", mode)
	}
	return mode
}

// enableReview clears this clone's `off` opinion: the other half of the disable
// journeys, and the point where issue #1877's re-enable sequence begins. It
// restores the fixture's global `on` rather than asserting one of its own,
// because a repository-scoped source may only ever disable.
func (harness *organicHarness) enableReview() organicModeResult {
	harness.t.Helper()
	payload := harness.run("review", "mode", "enable", "--cwd", harness.repo.worktree, "--scope", "clone", "--json")
	var mode organicModeResult
	if err := json.Unmarshal(payload, &mode); err != nil {
		harness.t.Fatalf("decode review mode: %v\n%s", err, payload)
	}
	return mode
}

// organicNamedContinuation returns the argument tokens of the first
// `brunnr-code ...` command a product message names, read exactly as an operator
// would: to the end of the line, stopping at the first `<placeholder>` whose
// value the operator supplies.
func organicNamedContinuation(t *testing.T, message string) []string {
	t.Helper()
	const product = "brunnr-code "
	index := strings.Index(message, product)
	if index < 0 {
		t.Fatalf("message names no runnable brunnr-code command: %q", message)
	}
	tail := message[index+len(product):]
	if cut := strings.IndexAny(tail, "\n"); cut >= 0 {
		tail = tail[:cut]
	}
	tokens := []string{}
	for _, token := range strings.Fields(tail) {
		token = strings.Trim(token, ",.;:'\"`)]")
		if token == "" || strings.HasPrefix(token, "<") {
			break
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		t.Fatalf("message names no runnable brunnr-code command: %q", message)
	}
	return tokens
}

// runNamedReviewStart dispatches a `brunnr-code review start ...` continuation
// read out of a product message, with the working directory already at the
// repository so the invocation runs exactly as printed. extra carries only an
// operator-supplied placeholder value the message asked for.
func (harness *organicHarness) runNamedReviewStart(tokens []string, extra ...string) organicStartResult {
	harness.t.Helper()
	if len(tokens) < 2 || tokens[0] != "review" || tokens[1] != "start" {
		harness.t.Fatalf("named continuation is %v, want brunnr-code review start", tokens)
	}
	payload := harness.run(append(append([]string{}, tokens...), extra...)...)
	var started organicStartResult
	if err := json.Unmarshal(payload, &started); err != nil {
		harness.t.Fatalf("decode named review start: %v\n%s", err, payload)
	}
	return started
}

// reviewModeGenerations lists the clone-local kill-switch compare-and-swap
// records. Their count is how a rejected operation proves it wrote nothing.
func (harness *organicHarness) reviewModeGenerations() []string {
	harness.t.Helper()
	root := filepath.Join(harness.commonDir(), "brunnr-code", "review-mode", "rar-authority", "v1", "rdd-mode")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		harness.t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "gen-") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// lineageDigest fingerprints every authority file of one lineage so a replay can
// prove it changed nothing at all, not merely that it reported the same state.
func (harness *organicHarness) lineageDigest(lineage string) string {
	harness.t.Helper()
	root := filepath.Join(harness.commonDir(), "brunnr-code", "review-transactions", "v2", lineage)
	var builder strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		fmt.Fprintf(&builder, "%s\x00%x\n", filepath.ToSlash(relative), payload)
		return nil
	})
	if err != nil {
		harness.t.Fatalf("digest review lineage %q: %v", lineage, err)
	}
	return builder.String()
}

func (harness *organicHarness) assertSingleReviewLineage(expected string) {
	harness.t.Helper()
	root := filepath.Join(harness.commonDir(), "brunnr-code", "review-transactions", "v2")
	entries, err := os.ReadDir(root)
	if err != nil {
		harness.t.Fatalf("read review authority: %v", err)
	}
	lineages := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			lineages = append(lineages, entry.Name())
		}
	}
	if len(lineages) != 1 || lineages[0] != expected {
		harness.t.Fatalf("review lineages = %v, want exactly [%s]", lineages, expected)
	}
}

// assertNoRetiredArtifacts is the proposal's core claim: direct and delegated
// work never create trace or evaluation state.
func (harness *organicHarness) assertNoRetiredArtifacts() {
	harness.t.Helper()
	if name, found := harness.retiredArtifact(); found {
		harness.t.Fatalf("organic implementation created forbidden trace/evaluation artifact %q", name)
	}
}

func (harness *organicHarness) retiredArtifact() (string, bool) {
	harness.t.Helper()
	root := filepath.Join(harness.commonDir(), "brunnr-code")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		harness.t.Fatal(err)
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if name == "trace" || name == "evaluation" {
			return filepath.Join(root, entry.Name()), true
		}
	}
	return "", false
}

// commonDir resolves the repository the way the product does, so an aliased or
// relative invocation cannot silently point the assertions at another clone.
func (harness *organicHarness) commonDir() string {
	harness.t.Helper()
	common := harness.git("rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(harness.repo.worktree, common)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(common))
	if err != nil {
		harness.t.Fatal(err)
	}
	return resolved
}

func (harness *organicHarness) runActor(role, path, body, message, marker string) {
	harness.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(ctx, os.Args[0])
	command.Dir = harness.repo.worktree
	command.Env = append(harness.environment(),
		organicActorRoleEnvironment+"="+role,
		organicActorRepoEnvironment+"="+harness.repo.worktree,
		organicActorPathEnvironment+"="+path,
		organicActorBodyEnvironment+"="+body,
		organicActorMessageEnvironment+"="+message,
		organicActorBinaryEnvironment+"="+organicBinary,
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		harness.t.Fatalf("%s actor: %v\nstdout:\n%s\nstderr:\n%s", role, err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), marker) {
		harness.t.Fatalf("%s actor did not report %q: %s", role, marker, stdout.String())
	}
}

// writeFiles writes candidate files and declares them. Since #2394 a new file
// only enters the review candidate once the user put it in the index, so a
// journey that means to have its files reviewed has to say so the same way a
// real user does.
func (harness *organicHarness) writeFiles(files map[string]string) {
	harness.t.Helper()
	for relative, body := range files {
		target := filepath.Join(harness.repo.worktree, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			harness.t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			harness.t.Fatal(err)
		}
		harness.git("add", "--", relative)
	}
}

func (harness *organicHarness) writeJSON(name string, value any) string {
	harness.t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		harness.t.Fatal(err)
	}
	// Review inputs live outside the repository so they never become part of the
	// candidate they describe.
	path := filepath.Join(harness.t.TempDir(), name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		harness.t.Fatal(err)
	}
	return path
}

func (harness *organicHarness) writeRawReviewerResult(name string, payload []byte) string {
	harness.t.Helper()
	path := filepath.Join(harness.t.TempDir(), name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		harness.t.Fatal(err)
	}
	return path
}

func (harness *organicHarness) writeEvidence() string {
	harness.t.Helper()
	path := filepath.Join(harness.t.TempDir(), "evidence.txt")
	if err := os.WriteFile(path, []byte("focused and full verification: pass\n"), 0o600); err != nil {
		harness.t.Fatal(err)
	}
	return path
}

func (harness *organicHarness) git(arguments ...string) string {
	harness.t.Helper()
	output, err := organicGitOutput(context.Background(), harness.repo.worktree, arguments...)
	if err != nil {
		harness.t.Fatal(err)
	}
	return output
}

func (harness *organicHarness) bareGit(arguments ...string) string {
	harness.t.Helper()
	output, err := organicBareGitOutput(context.Background(), harness.repo.bare, arguments...)
	if err != nil {
		harness.t.Fatal(err)
	}
	return output
}

// pushWithLease publishes under compare-and-swap against the exact revision the
// candidate was reviewed on top of.
func (harness *organicHarness) pushWithLease(expected string) {
	harness.t.Helper()
	harness.git("push", "--quiet", "--force-with-lease=refs/heads/main:"+expected, "origin", "HEAD:refs/heads/main")
	local := harness.git("rev-parse", "HEAD")
	if remote := harness.bareGit("rev-parse", "refs/heads/main"); remote != local {
		harness.t.Fatalf("remote ref = %s, want the delivered candidate %s", remote, local)
	}
}

// assertStaleLeaseIsRejected proves the publication really is a compare-and-swap.
// It needs something to publish, because an up-to-date push would succeed
// without ever consulting the lease and would prove nothing.
func (harness *organicHarness) assertStaleLeaseIsRejected(stale string) {
	harness.t.Helper()
	before := harness.bareGit("rev-parse", "refs/heads/main")
	harness.writeFiles(map[string]string{"docs/lease-probe.md": "lease probe\n"})
	harness.git("add", "--", "docs/lease-probe.md")
	harness.git("commit", "-q", "-m", "test: probe the publication lease")
	if _, err := organicGitOutput(
		context.Background(), harness.repo.worktree,
		"push", "--quiet", "--force-with-lease=refs/heads/main:"+stale, "origin", "HEAD:refs/heads/main",
	); err == nil {
		harness.t.Fatal("a stale compare-and-swap lease was accepted")
	}
	if after := harness.bareGit("rev-parse", "refs/heads/main"); after != before {
		harness.t.Fatalf("a rejected compare-and-swap still moved the remote: %s -> %s", before, after)
	}
	harness.git("reset", "--quiet", "--hard", before)
}

// assertRemoteBlob proves delivery reached the bare repository as exact content,
// not merely as a moved ref.
func (harness *organicHarness) assertRemoteBlob(path, body string) {
	harness.t.Helper()
	entry := harness.bareGit("ls-tree", "refs/heads/main", "--", path)
	if entry == "" {
		harness.t.Fatalf("delivered path %q is absent from the remote tree", path)
	}
	fields := strings.Fields(entry)
	if len(fields) < 3 {
		harness.t.Fatalf("unreadable remote tree entry %q", entry)
	}
	if fields[0] != "100644" {
		harness.t.Fatalf("delivered mode = %q, want 100644", fields[0])
	}
	blob := harness.bareGit("cat-file", "blob", fields[2])
	if blob != strings.TrimRight(body, "\n") {
		harness.t.Fatalf("delivered blob content differs:\nwant:\n%s\ngot:\n%s", body, blob)
	}
	tree := harness.bareGit("rev-parse", "refs/heads/main^{tree}")
	if localTree := harness.git("rev-parse", "HEAD^{tree}"); tree != localTree {
		harness.t.Fatalf("delivered tree = %s, want the reviewed tree %s", tree, localTree)
	}
}

func (harness *organicHarness) assertOnlyMainRef() {
	harness.t.Helper()
	refs := harness.bareGit("for-each-ref", "--format=%(refname)")
	if refs != "refs/heads/main" {
		harness.t.Fatalf("bare repository refs = %q, want only refs/heads/main", refs)
	}
}

// ---------------------------------------------------------------------------
// Wire projections
// ---------------------------------------------------------------------------

type organicStartResult struct {
	Operation        string   `json:"operation"`
	Action           string   `json:"action"`
	LensesRequired   bool     `json:"lenses_required"`
	LineageID        string   `json:"lineage_id"`
	State            string   `json:"state"`
	RiskLevel        string   `json:"risk_level"`
	SelectedLenses   []string `json:"selected_lenses"`
	ChangedFiles     int      `json:"changed_files"`
	ChangedLines     int      `json:"changed_lines"`
	CorrectionBudget int      `json:"correction_budget"`
	Projection       string   `json:"projection"`
	TargetMode       string   `json:"target_mode,omitempty"`
	TargetIdentity   string   `json:"target_identity"`
	BaseTree         string   `json:"base_tree,omitempty"`

	Repository      *organicStartRepositoryContext `json:"repository_context,omitempty"`
	Acknowledgement *organicProviderExecute        `json:"acknowledgement,omitempty"`
	statusSelectors []string
	// Hint is the informational recovery pointer an empty-candidate start
	// carries; the re-enable journey follows it verbatim.
	Hint string `json:"hint"`
}

type organicStartRepositoryContext struct {
	TargetIdentity string `json:"target_identity"`
}

func (result organicStartResult) targetIdentity() string {
	if result.TargetIdentity != "" {
		return result.TargetIdentity
	}
	if result.Repository != nil {
		return result.Repository.TargetIdentity
	}
	return ""
}

type organicFinalizeResult struct {
	Operation       string                  `json:"operation"`
	LineageID       string                  `json:"lineage_id"`
	State           string                  `json:"state"`
	Action          string                  `json:"action"`
	StoreRevision   string                  `json:"store_revision"`
	ReceiptPath     string                  `json:"receipt_path"`
	Acknowledgement *organicProviderExecute `json:"acknowledgement,omitempty"`
	statusSelectors []string
}

type organicGateResult struct {
	Schema  string             `json:"schema"`
	Result  string             `json:"result"`
	Allowed bool               `json:"allowed"`
	Action  string             `json:"action"`
	Reason  string             `json:"reason"`
	Context organicGateContext `json:"context"`
	// Delivery carries the disposition the shipped agent guidance promises. The
	// guidance tells all 16 adapters to expect this token under a disabled
	// switch, so the wire has to actually produce it.
	Delivery string `json:"delivery"`
}

type organicGateContext struct {
	Gate          string             `json:"gate"`
	LineageID     string             `json:"lineage_id"`
	StoreRevision string             `json:"store_revision"`
	BundleDigest  string             `json:"bundle_digest"`
	BaseTree      string             `json:"base_tree"`
	CandidateTree string             `json:"candidate_tree"`
	Denial        *organicGateDenial `json:"denial"`
}

type organicGateDenial struct {
	Stage string `json:"stage"`
	Code  string `json:"code"`
}

type organicModeResult struct {
	Schema    string `json:"schema"`
	Operation string `json:"operation"`
	Scope     string `json:"scope"`
	Status    struct {
		Schema     string `json:"schema"`
		Global     string `json:"global"`
		CloneLocal string `json:"clone_local"`
		Effective  string `json:"effective"`
		Source     string `json:"source"`
		Revision   string `json:"revision"`
	} `json:"status"`
}

type organicReviewerResult struct {
	SubjectHash string             `json:"subject_hash,omitempty"`
	Inspection  *organicInspection `json:"inspection,omitempty"`
	Lens        string             `json:"lens"`
	Findings    []organicFinding   `json:"findings"`
	Evidence    []string           `json:"evidence"`
}

type organicFinding struct {
	ID                string   `json:"id,omitempty"`
	Location          string   `json:"location"`
	Severity          string   `json:"severity"`
	Claim             string   `json:"claim"`
	ProofRefs         []string `json:"proof_refs"`
	EvidenceClass     string   `json:"evidence_class"`
	CausalDisposition string   `json:"causal_disposition"`
}

type organicValidationCheck struct {
	Passed   bool     `json:"passed"`
	Evidence []string `json:"evidence"`
}

type organicValidationResult struct {
	TargetedValidationRequestHash string                 `json:"targeted_validation_request_hash,omitempty"`
	CorrectionTargetIdentity      string                 `json:"correction_target_identity,omitempty"`
	OriginalCriteria              organicValidationCheck `json:"original_criteria"`
	CorrectionRegression          organicValidationCheck `json:"correction_regression"`
	FollowUps                     []any                  `json:"follow_ups"`
}

type organicCorrectionStatus struct {
	Authority struct {
		Revision string `json:"revision"`
	} `json:"authority"`
	NextTransition *struct {
		Kind       string `json:"kind"`
		ReasonCode string `json:"reason_code"`
		Collect    *struct {
			Inputs []struct {
				CaptureOperation string `json:"capture_operation"`
				Arguments        []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"arguments"`
			} `json:"inputs"`
		} `json:"collect"`
	} `json:"next_transition"`
	ValidationRequest *struct {
		RequestHash              string `json:"request_hash"`
		CorrectionTargetIdentity string `json:"correction_target_identity"`
	} `json:"validation_request"`
}

func harnessCorrectionStatus(t *testing.T, harness *organicHarness, lineage string) organicCorrectionStatus {
	t.Helper()
	payload := harness.run(
		"review", "status", "--cwd", harness.repo.worktree, "--lineage", lineage,
		"--contract", "brunnr-code.review-integration/v2", "--next-transition",
	)
	var status organicCorrectionStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("decode correction status: %v\n%s", err, payload)
	}
	return status
}

// ---------------------------------------------------------------------------
// Repository fixtures and shared utilities
// ---------------------------------------------------------------------------

func initOrganicRepository(t *testing.T) organicRepository {
	t.Helper()
	repo := t.TempDir()
	for _, arguments := range [][]string{
		{"init", "--quiet", "--initial-branch=main", "."},
		{"config", "user.name", "Organic E2E"},
		{"config", "user.email", "organic-e2e@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := organicGitOutput(context.Background(), repo, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("organic runtime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := organicGitOutput(context.Background(), repo, "add", "--", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := organicGitOutput(context.Background(), repo, "commit", "-q", "-m", "test: seed the organic repository"); err != nil {
		t.Fatal(err)
	}
	baseRevision, err := organicGitOutput(context.Background(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	bare := filepath.Join(t.TempDir(), "origin.git")
	if _, err := organicGitOutput(context.Background(), repo, "init", "--bare", "--quiet", bare); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"remote", "add", "origin", bare},
		{"push", "--quiet", "--set-upstream", "origin", "main:refs/heads/main"},
	} {
		if _, err := organicGitOutput(context.Background(), repo, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	if err := requireOrganicOnlyMainRef(context.Background(), bare); err != nil {
		t.Fatal(err)
	}
	return organicRepository{worktree: repo, bare: bare, baseRevision: baseRevision}
}

func organicGitOutput(parent context.Context, repo string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(ctx, "git", append([]string{"-C", repo}, arguments...)...)
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git -C %q %v: %w\n%s", repo, arguments, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func organicBareGitOutput(parent context.Context, bare string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(ctx, "git", append([]string{"--git-dir=" + bare}, arguments...)...)
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git --git-dir=%q %v: %w\n%s", bare, arguments, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func requireOrganicOnlyMainRef(parent context.Context, bare string) error {
	refs, err := organicBareGitOutput(parent, bare, "for-each-ref", "--format=%(refname)")
	if err != nil {
		return err
	}
	if refs != "refs/heads/main" {
		return fmt.Errorf("bare repository refs = %q, want only refs/heads/main", refs)
	}
	return nil
}

func sameOrganicDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && leftInfo.IsDir() && rightInfo.IsDir() && os.SameFile(leftInfo, rightInfo)
}

func organicCommandContext(ctx context.Context, name string, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, arguments...)
	command.WaitDelay = organicCommandWaitDelay
	return command
}

func organicModuleRoot() (string, error) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve the organic test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..")), nil
}

// buildOrganicBinary compiles the product once for the whole package. Every
// journey drives that one binary, so a per-journey build would only buy slower
// feedback for the same proof.
func buildOrganicBinary(workspace string) (string, error) {
	moduleRoot, err := organicModuleRoot()
	if err != nil {
		return "", err
	}
	name := "brunnr-code"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(workspace, name)
	ctx, cancel := context.WithTimeout(context.Background(), organicSetupTimeout)
	defer cancel()
	command := organicCommandContext(ctx, "go", "build", "-trimpath", "-o", path, "./cmd/brunnr-code")
	command.Dir = moduleRoot
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build the brunnr-code test binary: %w\n%s", err, output)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("built brunnr-code binary %q is unusable: %v", path, err)
	}
	return path, nil
}

func organicLines(prefix string, count int) string {
	var builder strings.Builder
	for index := 1; index <= count; index++ {
		fmt.Fprintf(&builder, "%s %03d\n", prefix, index)
	}
	return builder.String()
}

func organicMechanicalFiles(files, linesPerFile int) map[string]string {
	rendered := make(map[string]string, files)
	for index := 1; index <= files; index++ {
		var builder strings.Builder
		builder.WriteString("package mechanical\n\n")
		for line := 1; line <= linesPerFile; line++ {
			fmt.Fprintf(&builder, "// mechanical line %03d\n", line)
		}
		rendered[fmt.Sprintf("internal/mechanical/unit%02d.go", index)] = builder.String()
	}
	return rendered
}

// organicLimitSource renders the same unit twice with exactly one differing
// line, so the bounded correction stays inside the frozen budget and the budget
// itself is what the assertions are about.
func organicLimitSource(state string) string {
	var builder strings.Builder
	builder.WriteString("package feature\n\n")
	for index := 1; index <= 12; index++ {
		fmt.Fprintf(&builder, "// Limit documents the bounded terminal value, note %02d.\n", index)
	}
	builder.WriteString("func Limit() int {\n")
	fmt.Fprintf(&builder, "\treturn %s\n", map[string]string{"broken": "-1", "fixed": "1"}[state])
	builder.WriteString("}\n")
	return builder.String()
}

func equalOrganicStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Pinned real-agent journeys
// ---------------------------------------------------------------------------

// organicActorToolCommand runs the compiled actor process from the agent's own
// bash tool, so the implementation step is a real child process of a real agent.
func organicActorToolCommand(t *testing.T) string {
	t.Helper()
	return organicToolCommand(t, "BRUNNR_CODE_ORGANIC_ACTOR_EXECUTABLE")
}

func organicReviewToolCommand(t *testing.T, arguments ...string) string {
	t.Helper()
	return organicToolCommand(t, "BRUNNR_CODE_ORGANIC_BINARY", arguments...)
}

// organicToolCommand turns one fixture-authored argv into the string the
// agent's bash tool executes, without round-tripping the argv through a shell
// flavour we do not control.
//
// On POSIX systems the agent's tool shell is sh-compatible, so the argv is
// authored directly in sh syntax — byte-identical to what the journeys always
// ran on Linux. On Windows the agent's tool shell is PowerShell (or cmd),
// neither of which parses POSIX single-quoting (PowerShell fails with
// "ParserError: Unexpected token" on a single-quoted argument such as
// 'review'), so the argv is baked by Go into a generated .cmd trampoline
// and the command becomes that script's bare path: a single token that
// PowerShell and cmd both resolve as a plain invocation.
func organicToolCommand(t *testing.T, binaryEnvironment string, arguments ...string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		command := `"$` + binaryEnvironment + `"`
		for _, argument := range arguments {
			command += " '" + strings.ReplaceAll(argument, "'", `'\''`) + "'"
		}
		return command
	}

	invocation := `"%` + binaryEnvironment + `%"`
	for _, argument := range arguments {
		if strings.ContainsAny(argument, `"%`) {
			t.Fatalf("tool argument %q cannot be embedded safely in a cmd trampoline", argument)
		}
		invocation += ` "` + argument + `"`
	}
	script := filepath.Join(t.TempDir(), "tool.cmd")
	if strings.ContainsAny(script, " \t'\"") {
		t.Fatalf("tool trampoline path %q would itself need shell quoting", script)
	}
	content := strings.Join([]string{"@echo off", invocation, "exit /b %ERRORLEVEL%"}, "\r\n") + "\r\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

type openAIRequest struct {
	Messages []openAIMessage   `json:"messages"`
	Tools    []json.RawMessage `json:"tools"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func organicFinishChunk(reason string) map[string]any {
	return map[string]any{
		"id": "chat", "object": "chat.completion.chunk", "created": 0, "model": "fixture",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	}
}

func requireOrganicExecutable(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("required executable %s: %v", name, err)
	}
}

func requireOrganicExecutableVersion(t *testing.T, name, expected string) {
	t.Helper()
	requireOrganicExecutable(t, name)
	ctx, cancel := context.WithTimeout(context.Background(), organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(ctx, name, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", name, err, output)
	}
	if strings.TrimSpace(string(output)) != expected {
		t.Fatalf("%s version = %q, want %q", name, strings.TrimSpace(string(output)), expected)
	}
}

func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, part := range value {
			encoded, _ := json.Marshal(part)
			builder.Write(encoded)
		}
		return builder.String()
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}
