package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/reviewtransaction"
	"github.com/carsimax/brunnr-code/internal/state"
)

// TestManagedReviewerAssetProvenanceRefusesOnlyRecordedSkew pins the two
// shapes the refusal must NOT take. Only a recorded digest that disagrees is
// stale; a home that never installed anything has no managed assets to be
// stale, and refusing it would block every `go install` user from reviewing
// while telling them to run a sync that would fix nothing.
func TestManagedReviewerAssetProvenanceRefusesOnlyRecordedSkew(t *testing.T) {
	digest, err := managedAssetDigest()
	requireManagedAssetProvenanceNoError(t, err)

	for name, persist := range map[string]func(string){
		"no state file at all": func(string) {},
		"state without a recorded digest": func(home string) {
			requireManagedAssetProvenanceNoError(t, state.Write(home, state.InstallState{InstalledAgents: []string{"claude-code"}}))
		},
		"digest matching this binary's assets": func(home string) {
			requireManagedAssetProvenanceNoError(t, state.Write(home, state.InstallState{ManagedAssetDigest: digest}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			persist(home)
			if err := authorizeManagedReviewerAssets(); err != nil {
				t.Fatalf("authorize = %v, want no refusal", err)
			}
		})
	}

	t.Run("recorded digest that disagrees", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		staleManagedReviewerAssets(t, home)
		requireManagedAssetProvenanceError(t, authorizeManagedReviewerAssets(), managedAssetProvenanceRefusal)
	})
}

func TestNegotiatedReviewStartClassifiesStaleManagedAssetsBeforeAuthority(t *testing.T) {
	home, repo := reviewEnabledHome(t), initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/stale-assets.md", "# Candidate\n", 0o644)
	staleManagedReviewerAssets(t, home)

	var output bytes.Buffer
	err := RunReview(boundNegotiatedStartArgs(t, []string{
		"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", "claude-code", "--consent", "granted",
	}), &output)
	if err == nil {
		t.Fatalf("stale managed assets START succeeded: %s", output.String())
	}
	failure := decodeReviewIntegrationFailure(t, output.Bytes())
	if failure.Operation != "review.start" || failure.Phase != "preflight" ||
		failure.Code != "managed_assets_outdated" || failure.MutationOutcome != ReviewMutationNotStarted ||
		failure.NextAction != "stop" {
		t.Fatalf("stale managed assets failure = %#v", failure)
	}
	if failure.Cause != managedAssetProvenanceRefusal {
		t.Fatalf("stale managed assets cause = %q, want %q", failure.Cause, managedAssetProvenanceRefusal)
	}
	// #3299, #4170: the failure names the exact candidate-preserving sync
	// continuation instead of leaving the caller to guess "run sync" from the
	// cause prose. #4434: the command is anchored to the invoking executable,
	// so it cannot resolve to a different `brunnr-code` through PATH.
	if failure.Continuation == nil || failure.Continuation.Operation != "sync" ||
		failure.Continuation.Command != managedAssetsTestContinuationCommand(t, "claude-code") || failure.Continuation.Agent != "claude-code" ||
		len(failure.Continuation.StaleAssets) != 1 || failure.Continuation.StaleAssets[0] != "sha256:stale" {
		t.Fatalf("stale managed assets continuation = %#v", failure.Continuation)
	}
	if err := failure.Validate(); err != nil {
		t.Fatalf("stale managed assets failure does not satisfy its published contract: %v", err)
	}
	malformedFailure := failure
	malformedContinuation := *failure.Continuation
	malformedContinuation.Command = "'/tmp/brunnr\ncode' sync --agent claude-code"
	malformedFailure.Continuation = &malformedContinuation
	if err := malformedFailure.Validate(); err == nil {
		t.Fatal("FAILURE accepted a multiline managed-assets continuation command")
	}
}

func TestNegotiatedReviewStartWithCurrentManagedAssetsStillStarts(t *testing.T) {
	home, repo := reviewEnabledHome(t), initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/current-assets.md", "# Candidate\n", 0o644)
	digest, err := managedAssetDigest()
	requireManagedAssetProvenanceNoError(t, err)
	recordManagedAssetDigest(t, home, digest)

	var output bytes.Buffer
	err = RunReview(boundNegotiatedStartArgs(t, []string{
		"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", "claude-code", "--consent", "granted",
	}), &output)
	if err != nil {
		t.Fatalf("current managed assets START refused: %v\n%s", err, output.String())
	}
	started := decodeNegotiatedReviewStart(t, output.Bytes())
	if err := started.Validate(); err != nil || started.LineageID == "" {
		t.Fatalf("current managed assets START = %#v, validate = %v", started, err)
	}
}

// TestNegotiatedStatusReportsManagedAssetsOutdatedBeforeOfferingStart is the
// RED-first proof for #3299/#4170: selectorless STATUS must classify a stale
// managed-asset digest and name the exact sync continuation BEFORE it ever
// offers a START that preflight would refuse anyway. Executing the returned
// START used to be the only way to discover the skew, and its failure named
// no continuation the caller could run instead of guessing "run sync" from
// prose.
func TestNegotiatedStatusReportsManagedAssetsOutdatedBeforeOfferingStart(t *testing.T) {
	home, repo := reviewEnabledHome(t), initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/status-stale-assets.md", "# Candidate\n", 0o644)
	staleManagedReviewerAssets(t, home)

	var output bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &output); err != nil {
		t.Fatalf("stale managed assets STATUS: %v\n%s", err, output.String())
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, output.Bytes(), &status)
	if status.Applicability != reviewtransaction.TargetApplicabilityUnrelated || status.NextTransition == nil ||
		status.NextTransition.Kind != reviewNextTransitionStop || status.NextTransition.ReasonCode != "managed_assets_outdated" ||
		status.NextTransition.Execute != nil {
		t.Fatalf("stale managed assets STATUS transition = %#v", status.NextTransition)
	}
	continuation := status.NextTransition.Continuation
	if continuation == nil || continuation.Operation != "sync" || continuation.Command != managedAssetsTestContinuationCommand(t, "claude-code") ||
		continuation.Agent != "claude-code" || len(continuation.StaleAssets) != 1 || continuation.StaleAssets[0] != "sha256:stale" {
		t.Fatalf("stale managed assets STATUS continuation = %#v", continuation)
	}
	statusV9Schema := compileWholeNativeStatusSchema(t, "status-v9.schema.json")
	validatePublishedReviewSchema(t, statusV9Schema, output.Bytes())

	// Once the recorded digest converges with this binary's, the very same
	// candidate must be offered again: the skew was the only thing blocking
	// it, and nothing about the candidate itself changed.
	digest, err := managedAssetDigest()
	requireManagedAssetProvenanceNoError(t, err)
	recordManagedAssetDigest(t, home, digest)

	var convergedOutput bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &convergedOutput); err != nil {
		t.Fatalf("converged managed assets STATUS: %v\n%s", err, convergedOutput.String())
	}
	var converged ReviewTargetStatusResult
	decodeStrictReviewJSON(t, convergedOutput.Bytes(), &converged)
	if converged.NextTransition == nil || converged.NextTransition.Kind != reviewNextTransitionExecute ||
		converged.NextTransition.ReasonCode != "fresh_target_ready" || converged.NextTransition.Execute == nil ||
		converged.NextTransition.Execute.Operation != "review.start" {
		t.Fatalf("converged managed assets STATUS transition = %#v", converged.NextTransition)
	}
}

// TestManagedAssetsStopTransitionCarriesExactlyOneSignal is the RED-first
// proof for the inconclusive review finding on #3299/#4170: the negotiated
// STATUS envelope must carry exactly one signal for a stale managed-asset
// digest -- the typed `stop`/`managed_assets_outdated` transition with its
// sync continuation -- and Validate() must refuse either way this could
// drift: the continuation missing from the stop that requires it, or the
// continuation attached to a transition it does not belong to (a producer
// bug that would let a caller read two disagreeing exits from one envelope).
func TestManagedAssetsStopTransitionCarriesExactlyOneSignal(t *testing.T) {
	home, repo := reviewEnabledHome(t), initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/dual-signal.md", "# Candidate\n", 0o644)
	staleManagedReviewerAssets(t, home)

	var staleOutput bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &staleOutput); err != nil {
		t.Fatalf("stale managed assets STATUS: %v\n%s", err, staleOutput.String())
	}
	var stale ReviewTargetStatusResult
	decodeStrictReviewJSON(t, staleOutput.Bytes(), &stale)
	if stale.NextTransition == nil || stale.NextTransition.Kind != reviewNextTransitionStop ||
		stale.NextTransition.ReasonCode != "managed_assets_outdated" || stale.NextTransition.Continuation == nil {
		t.Fatalf("baseline stale managed assets STATUS = %#v", stale.NextTransition)
	}
	if err := stale.Validate(); err != nil {
		t.Fatalf("baseline stale managed assets STATUS should validate: %v", err)
	}
	malformedStatus := stale
	malformedTransition := *stale.NextTransition
	malformedContinuation := *stale.NextTransition.Continuation
	malformedContinuation.Command = "'/tmp/brunnr\rcode' sync --agent claude-code"
	malformedTransition.Continuation = &malformedContinuation
	malformedStatus.NextTransition = &malformedTransition
	if err := malformedStatus.Validate(); err == nil {
		t.Fatal("STATUS accepted a multiline managed-assets continuation command")
	}

	// A managed_assets_outdated stop without its continuation names no way
	// out at all: the caller cannot resolve it and cannot tell it apart from
	// a producer defect.
	missingContinuation := stale
	strippedTransition := *stale.NextTransition
	strippedTransition.Continuation = nil
	missingContinuation.NextTransition = &strippedTransition
	if err := missingContinuation.Validate(); err == nil {
		t.Fatal("STATUS accepted a managed_assets_outdated stop with no sync continuation")
	}

	digest, err := managedAssetDigest()
	requireManagedAssetProvenanceNoError(t, err)
	recordManagedAssetDigest(t, home, digest)
	var convergedOutput bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &convergedOutput); err != nil {
		t.Fatalf("converged managed assets STATUS: %v\n%s", err, convergedOutput.String())
	}
	var converged ReviewTargetStatusResult
	decodeStrictReviewJSON(t, convergedOutput.Bytes(), &converged)
	if converged.NextTransition == nil || converged.NextTransition.Kind != reviewNextTransitionExecute {
		t.Fatalf("converged managed assets STATUS = %#v", converged.NextTransition)
	}

	// A continuation on an executable START is a second, disagreeing signal:
	// nothing about a fresh_target_ready execute names a sync problem, so a
	// caller reading both would not know which one to trust.
	executeWithContinuation := converged
	bogusTransition := *converged.NextTransition
	bogusTransition.Continuation = &ReviewStopContinuation{Operation: "sync", Command: "brunnr-code sync --agent claude-code", Agent: "claude-code"}
	executeWithContinuation.NextTransition = &bogusTransition
	if err := executeWithContinuation.Validate(); err == nil {
		t.Fatal("STATUS accepted a sync continuation attached to an executable START transition")
	}
}

// TestManagedAssetsContinuationUsesInvokingExecutable is the RED-first proof
// for #4434: a STATUS or START refusal produced by one Brunnr-Code binary must
// offer a continuation that runs THAT binary, not whatever `brunnr-code` happens
// to resolve to on PATH. The continuation used to hard-code the unqualified
// executable name while describing itself as the exact runnable recovery, so
// with a different global binary first on PATH the offered sync wrote that
// binary's digest and the refusing binary never converged: repeating the exact
// advertised continuation could not clear its own refusal.
func TestManagedAssetsContinuationUsesInvokingExecutable(t *testing.T) {
	invoking, err := os.Executable()
	if err != nil {
		t.Skipf("invoking executable unresolvable: %v", err)
	}
	// The expectation renders the executable token independently of the
	// production helper -- a byte-scan allowlist instead of the shared regex,
	// with the same platform dispatch -- so the two cannot agree by
	// construction. A Windows path of backslashes is quoted here exactly as
	// production quotes it, because backslash is outside the safe bare class
	// on every platform.
	quoted := func(path string) string {
		bare := path != ""
		for i := 0; i < len(path); i++ {
			c := path[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			case c == '/' || c == '.' || c == '_' || c == '+' || c == '=' || c == '@' || c == ':' || c == ',' || c == '-':
			default:
				bare = false
			}
		}
		if bare {
			return path
		}
		return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	}

	// Unit: the rendered command is rooted at whichever executable resolution
	// reports, using one exact encoding: paths quote with single quotes (no
	// POSIX shell expands anything inside them, so $ and backticks survive
	// literally), a path over the safe bare class stays bare, and
	// an unresolvable executable keeps the historical bare `brunnr-code` form
	// instead of guessing a path it cannot prove.
	for name, tc := range map[string]struct {
		executable func() (string, error)
		want       string
	}{
		"posix path with shell expansion uses single quotes": {
			executable: func() (string, error) { return `/opt/$HOME/brunnr-code`, nil },
			want:       `'/opt/$HOME/brunnr-code' sync --agent claude-code`,
		},
		"posix path with an embedded single quote escapes it": {
			executable: func() (string, error) { return `/opt/o'brien/brunnr-code`, nil },
			want:       `'/opt/o'\''brien/brunnr-code' sync --agent claude-code`,
		},
		"posix path over the safe bare class stays bare": {
			executable: func() (string, error) { return `/opt/brunnr-code/bin/brunnr-code`, nil },
			want:       `/opt/brunnr-code/bin/brunnr-code sync --agent claude-code`,
		},
		"unresolvable executable keeps the bare fallback": {
			executable: func() (string, error) { return "", errors.New("unresolvable") },
			want:       `brunnr-code sync --agent claude-code`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			previousPath := reviewManagedAssetsExecutablePath
			previousArgs := append([]string(nil), os.Args...)
			reviewManagedAssetsExecutablePath = tc.executable
			t.Cleanup(func() {
				reviewManagedAssetsExecutablePath = previousPath
				os.Args = previousArgs
			})
			// Keep these renderer cases focused on os.Executable. A relative
			// argv[0] remains only the final compatibility fallback.
			os.Args = append([]string{"brunnr-code"}, os.Args[1:]...)
			continuation := managedAssetsContinuation("claude-code", []string{"sha256:stale"})
			if continuation.Command != tc.want {
				t.Fatalf("continuation command = %q, want %q", continuation.Command, tc.want)
			}
			if !validManagedAssetsContinuationCommand(continuation.Command) {
				t.Fatalf("continuation command %q does not satisfy the published pattern", continuation.Command)
			}
		})
	}

	// The published contract itself must refuse a shell-significant bare
	// executable token: `/opt/$HOME/brunnr-code sync` unquoted is exactly the
	// shape a POSIX shell would expand into the wrong binary.
	if validManagedAssetsContinuationCommand(`/opt/$HOME/brunnr-code sync --agent pi`) {
		t.Fatal("published pattern accepted a shell-significant bare executable token")
	}

	// Round-trip identity: rendering a path with the production token
	// renderer and decoding it back with the splitter must return the
	// original path, for every quoting form the renderer can emit --
	// including the POSIX splice for an embedded apostrophe, whose \'
	// sequence must decode to a literal apostrophe in argv.
	for name, path := range map[string]string{
		"posix safe path":             `/opt/brunnr-code/bin/brunnr-code`,
		"posix path with expansion":   `/opt/$HOME/brunnr-code`,
		"posix path with apostrophe":  `/opt/o'brien/brunnr-code`,
		"posix path with backslashes": `/opt/we ird\x\brunnr-code`,
	} {
		t.Run("round-trip "+name, func(t *testing.T) {
			rendered := managedAssetsExecutableToken(path)
			argv := splitContinuationCommand(rendered + " sync --agent claude-code")
			if len(argv) != 4 || argv[0] != path || argv[1] != "sync" || argv[2] != "--agent" || argv[3] != "claude-code" {
				t.Fatalf("round-trip of %q rendered %q split to argv %q, want the original path plus the sync dispatch", path, rendered, argv)
			}
		})
	}

	// End to end: a stale-assets STATUS stop produced by THIS (test) binary
	// names THIS binary's own path in its continuation, so running the exact
	// advertised command cannot reach a different `brunnr-code` through PATH.
	home, repo := reviewEnabledHome(t), initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "docs/invoking-executable.md", "# Candidate\n", 0o644)
	staleManagedReviewerAssets(t, home)

	var output bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &output); err != nil {
		t.Fatalf("stale managed assets STATUS: %v\n%s", err, output.String())
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, output.Bytes(), &status)
	if status.NextTransition == nil || status.NextTransition.Continuation == nil {
		t.Fatalf("stale managed assets STATUS transition = %#v", status.NextTransition)
	}
	if want := quoted(invoking) + " sync --agent claude-code"; status.NextTransition.Continuation.Command != want {
		t.Fatalf("STATUS continuation command = %q, want %q (rooted at the invoking executable %q)",
			status.NextTransition.Continuation.Command, want, invoking)
	}
	// The executable-anchored command must still satisfy the published
	// continuation contract, not just this test's expectation.
	validatePublishedReviewSchema(t, compileWholeNativeStatusSchema(t, "status-v9.schema.json"), output.Bytes())

	// Convergence (#4434's invariant, not just the rendered shape): execute the
	// emitted continuation command exactly as printed -- through executable
	// startup, CLI dispatch, and flag parsing. This test binary IS the anchored
	// executable, and TestMain routes its CLI arguments to the real sync
	// dispatch under the stand-in guard, so the command line the refusal
	// advertised is the command line that runs, against the same captured home.
	argv := splitContinuationCommand(status.NextTransition.Continuation.Command)
	if len(argv) != 4 || argv[0] != invoking || argv[1] != "sync" || argv[2] != "--agent" || argv[3] != "claude-code" {
		t.Fatalf("continuation command %q split to argv %q, want the invoking executable %q plus the sync dispatch", status.NextTransition.Continuation.Command, argv, invoking)
	}
	var run *exec.Cmd
	// POSIX: run the printed line through a real shell exactly as an
	// operator would paste it, proving the quoting survives expansion
	// rather than merely decoding it ourselves.
	run = exec.Command("sh", "-c", status.NextTransition.Continuation.Command)
	run.Env = append(os.Environ(), "BRUNNR_CODE_TEST_CLI_STANDIN=1")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("executing the advertised continuation %q failed: %v\n%s", status.NextTransition.Continuation.Command, err, out)
	}
	var convergedOutput bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", "claude-code", "--next-transition",
	}, &convergedOutput); err != nil {
		t.Fatalf("post-continuation STATUS: %v\n%s", err, convergedOutput.String())
	}
	var converged ReviewTargetStatusResult
	decodeStrictReviewJSON(t, convergedOutput.Bytes(), &converged)
	if converged.NextTransition == nil || converged.NextTransition.Kind != reviewNextTransitionExecute ||
		converged.NextTransition.ReasonCode != "fresh_target_ready" {
		t.Fatalf("post-continuation STATUS transition = %#v, want execute/fresh_target_ready", converged.NextTransition)
	}
}

func TestManagedAssetsContinuationRejectsUnsafeExecutableIdentities(t *testing.T) {
	absoluteArgvZero := filepath.Join(t.TempDir(), "brunnr-code")
	for _, test := range []struct {
		name       string
		executable string
		resolveErr error
		argvZero   string
		want       string
	}{
		{
			name:       "resolver failure accepts absolute argv zero",
			resolveErr: errors.New("executable unavailable"),
			argvZero:   absoluteArgvZero,
			want:       managedAssetsExecutableToken(absoluteArgvZero) + " sync --agent claude-code",
		},
		{
			name:       "multiline resolver falls through to absolute argv zero",
			executable: filepath.Join(t.TempDir(), "brunnr\ncode"),
			argvZero:   absoluteArgvZero,
			want:       managedAssetsExecutableToken(absoluteArgvZero) + " sync --agent claude-code",
		},
		{
			name:       "multiline argv zero falls through to canonical fallback",
			resolveErr: errors.New("executable unavailable"),
			argvZero:   filepath.Join(t.TempDir(), "brunnr\rcode"),
			want:       "brunnr-code sync --agent claude-code",
		},
		{
			name:       "relative argv zero falls through to canonical fallback",
			resolveErr: errors.New("executable unavailable"),
			argvZero:   "brunnr-code",
			want:       "brunnr-code sync --agent claude-code",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			previousPath := reviewManagedAssetsExecutablePath
			previousArgs := append([]string(nil), os.Args...)
			reviewManagedAssetsExecutablePath = func() (string, error) { return test.executable, test.resolveErr }
			os.Args = append([]string{test.argvZero}, os.Args[1:]...)
			t.Cleanup(func() {
				reviewManagedAssetsExecutablePath = previousPath
				os.Args = previousArgs
			})

			continuation := managedAssetsContinuation("claude-code", nil)
			if strings.ContainsAny(continuation.Command, "\r\n") || continuation.Command != test.want {
				t.Fatalf("continuation command = %q, want %q", continuation.Command, test.want)
			}
		})
	}
}

func TestManagedAssetsPreflightDoesNotClassifyUnrelatedRuntimeRefusal(t *testing.T) {
	failure := newReviewIntegrationFailure("review.start", nil, errors.New("unrelated runtime refusal"))
	if failure.Code != "operation_outcome_unknown" || failure.Phase != "native_running" ||
		failure.MutationOutcome != ReviewMutationUnknown || failure.NextAction != "review.status" ||
		!strings.Contains(failure.Cause, "unrelated runtime refusal") {
		t.Fatalf("unrelated runtime failure = %#v", failure)
	}
}

// TestManagedAssetDigestIsStableAndAssetBound proves the digest is a property
// of the embedded assets rather than of the build, which is the whole reason
// it replaced the capabilities build identity: a rebuild that changes no asset
// must not invalidate an installation, and a test binary must be able to agree
// with a released one.
func TestManagedAssetDigestIsStableAndAssetBound(t *testing.T) {
	first, err := managedAssetDigest()
	requireManagedAssetProvenanceNoError(t, err)
	second, err := managedAssetDigest()
	requireManagedAssetProvenanceNoError(t, err)
	if first != second || first == "" {
		t.Fatalf("digest is not stable: %q then %q", first, second)
	}
	build, err := reviewCapabilitiesBuildIdentity(AppVersion)
	requireManagedAssetProvenanceNoError(t, err)
	if first == build.ID {
		t.Fatal("digest equals the build identity, so it still carries build metadata")
	}
}

// managedAssetsTestContinuationCommand renders the executable-anchored sync
// command the stale-managed-assets envelope tests expect: the invoking (test)
// binary plus the runtime agent, mirroring what managedAssetsContinuation must
// anchor to (#4434). The quoting cases themselves are owned by
// TestManagedAssetsContinuationUsesInvokingExecutable.
func managedAssetsTestContinuationCommand(t *testing.T, agent string) string {
	t.Helper()
	executable, err := os.Executable()
	requireManagedAssetProvenanceNoError(t, err)
	return managedAssetsExecutableToken(executable) + " sync --agent " + agent
}

// splitContinuationCommand splits one rendered continuation command into its
// argv, honoring both quoting forms the renderer may emit (POSIX single quotes
// and Windows double quotes) by unquoting, not by shelling out. It is written
// independently of the production renderer so the regression's execution path
// cannot agree with it by construction.
func splitContinuationCommand(command string) []string {
	var argv []string
	var token strings.Builder
	var open byte
	flush := func() {
		if token.Len() > 0 {
			argv = append(argv, token.String())
			token.Reset()
		}
	}
	for index := 0; index < len(command); index++ {
		c := command[index]
		switch {
		case open == '"':
			// Inside double quotes only a backslash-quote sequence is an
			// escape (the Windows form the renderer emits for an embedded
			// quote); every other backslash stays literal so Windows path
			// separators and doubled leading UNC backslashes survive intact.
			if c == '\\' && index+1 < len(command) && command[index+1] == '"' {
				index++
				token.WriteByte(command[index])
			} else if c == open {
				open = 0
			} else {
				token.WriteByte(c)
			}
		case open == '\'':
			// Everything is literal inside POSIX single quotes.
			if c == open {
				open = 0
			} else {
				token.WriteByte(c)
			}
		case c == '\\':
			// Outside any quote only a backslash-quote sequence is an
			// escape -- the splice the renderer emits between single-quoted
			// spans for an embedded apostrophe. A backslash before any other
			// character stays literal, so a path separator or a UNC lead
			// outside quotes is preserved rather than consumed.
			if index+1 < len(command) && (command[index+1] == '\'' || command[index+1] == '"') {
				index++
				token.WriteByte(command[index])
			} else {
				token.WriteByte(c)
			}
		case c == '\'' || c == '"':
			open = c
		case c == ' ' || c == '\t':
			flush()
		default:
			token.WriteByte(c)
		}
	}

	flush()
	return argv
}

// staleManagedReviewerAssets records an asset digest that disagrees with this
// binary's, which is the only skew the provenance guard refuses on.
//
// It reads the existing user state and rewrites only that one field. A blind
// state.Write would also erase the fixture's explicit global "on", silently
// replacing its declared precondition with the unset ON default.
func staleManagedReviewerAssets(t *testing.T, home string) {
	t.Helper()
	recordManagedAssetDigest(t, home, "sha256:stale")
}

// recordManagedAssetDigest rewrites only the recorded managed-asset digest,
// preserving every other opinion already persisted in the user's state.
func recordManagedAssetDigest(t *testing.T, home, digest string) {
	t.Helper()
	// A home with nothing persisted yet is an ordinary starting point here, so
	// an absent state file seeds an empty one rather than failing the fixture.
	persisted, err := state.Read(home)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	persisted.ManagedAssetDigest = digest
	requireManagedAssetProvenanceNoError(t, state.Write(home, persisted))
}
func requireManagedAssetProvenanceNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func requireManagedAssetProvenanceError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Fatalf("delivery error = %v, want %q", err, want)
	}
}
