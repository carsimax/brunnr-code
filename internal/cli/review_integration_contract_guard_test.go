package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/carsimax/brunnr-code/internal/assets"
)

// This file generalizes the pattern already proven by
// review_next_transition_docs_test.go (every stop reason code in source must
// have a docs continuation row) and review_next_transition_test.go's schema
// validation (an emitted payload is validated against the published JSON
// schema). It exists so the community-reported defect class from this task
// -- the shipped review-ledger contract promising a name or invocation no emitter
// actually backs, and an unnegotiated CLI response silently omitting data the
// contract requires without saying so -- cannot ship again.
//
// Scope (honesty contract): a fully general "extract every promise from
// English prose" checker is not tractable. This file covers three precise,
// mechanically-extracted, enumerable subsets instead:
//
//  1. Every brunnr-code.*/vN schema identifier literal the shipped review-ledger contract
//     quotes in backticks (Guard A).
//  2. Every `brunnr-code review <verb>` command the shipped review-ledger contract names
//     as required (Guard B).
//  3. The mode-completeness rule: every command that branches on negotiated
//     --contract (found via the reviewIntegrationNegotiation call sites, not
//     memorized) must be classified, and its classification must be backed by
//     a real source pattern (Guard C).
//
// What this deliberately does NOT cover: free-form prose promises (e.g. "the
// provider validates X"), field names inside Markdown tables that are not
// also backtick-quoted schema identifiers or brunnr-code commands, and doc
// sections outside the shipped review-ledger contract. A narrow guard that genuinely
// fails closed beats a broad one that silently passes.

// reviewIntegrationDocSchemaIDRegexp extracts every backtick-quoted
// brunnr-code.*/vN (or /vN.M) schema identity literal from the doc's prose.
var reviewIntegrationDocSchemaIDRegexp = regexp.MustCompile("`(brunnr-code\\.[a-z0-9.\\-]+/v[0-9]+(?:\\.[0-9]+)?)`")

// reviewIntegrationDocCommandVerbRegexp extracts every `brunnr-code review
// <verb>` command name the doc names, from both fenced code blocks and
// inline code spans (both use the same literal token sequence).
var reviewIntegrationDocCommandVerbRegexp = regexp.MustCompile(`brunnr-code review ([a-z][a-z-]*)`)

// readReviewIntegrationDoc returns the shipped review contract every runtime's
// rendered orchestrator prompt embeds; it is the documentation surface the
// guards below check.
func readReviewIntegrationDoc(t *testing.T) string {
	t.Helper()
	return assets.MustRead(reviewLedgerContractAsset)
}

// TestEveryDocumentedSchemaIdentityIsImplemented is Guard A. It fails closed
// in the one direction that matters for a documentation promise: a schema
// identity the shipped review-ledger contract quotes but no production Go source
// (internal/cli, internal/reviewtransaction) ever emits is a promise nothing
// satisfies.
func TestEveryDocumentedSchemaIdentityIsImplemented(t *testing.T) {
	docs := readReviewIntegrationDoc(t)
	matches := reviewIntegrationDocSchemaIDRegexp.FindAllStringSubmatch(docs, -1)
	if len(matches) == 0 {
		t.Fatal("found no brunnr-code.*/vN schema identifiers in the shipped review-ledger contract; the extraction regexp is stale")
	}
	ids := map[string]bool{}
	for _, match := range matches {
		ids[match[1]] = true
	}

	sources := reviewIntegrationProductionSources(t)
	for id := range ids {
		found := false
		for _, source := range sources {
			if strings.Contains(source, id) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the shipped review-ledger contract documents schema identity %q, but no production Go source under internal/cli or internal/reviewtransaction emits that literal", id)
		}
	}
}

// TestEveryDocumentedReviewCommandIsReal is Guard B. It fails closed when the
// doc names a `brunnr-code review <verb>` invocation that is not one of the
// verbs runReviewCommandContext/runReviewCommand actually dispatch --
// extracted mechanically from their case labels, not hand-maintained.
func TestEveryDocumentedReviewCommandIsReal(t *testing.T) {
	docs := readReviewIntegrationDoc(t)
	matches := reviewIntegrationDocCommandVerbRegexp.FindAllStringSubmatch(docs, -1)
	if len(matches) == 0 {
		t.Fatal("found no `brunnr-code review <verb>` commands in the shipped review-ledger contract; the extraction regexp is stale")
	}
	documented := map[string]bool{}
	for _, match := range matches {
		documented[match[1]] = true
	}

	// reviewDispatchableReviewVerbs, not reviewCommandDispatchVerbs: the app
	// layer routes `review mode` BEFORE the facade so the kill switch stays
	// reachable when review authority itself is unreadable, and a guard that
	// only reads the facade's switch calls that real command undocumented.
	// The sibling continuation guard learned this first; both now share the
	// one extractor that owns the complete answer.
	dispatched := reviewDispatchableReviewVerbs(t)
	for verb := range documented {
		if !dispatched[verb] {
			t.Errorf("the shipped review-ledger contract requires `brunnr-code review %s`, but no dispatch reaches it from the facade switches or the app pre-dispatch", verb)
		}
	}
}

// reviewIntegrationProductionSources reads every non-test .go file under
// internal/cli and internal/reviewtransaction, so Guard A only credits a
// schema identity actually implemented in production code -- never a test
// fixture that merely echoes the doc's own literal back.
func reviewIntegrationProductionSources(t *testing.T) []string {
	t.Helper()
	var sources []string
	for _, dir := range []string{".", "../reviewtransaction"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			payload, err := os.ReadFile(dir + "/" + name)
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, string(payload))
		}
	}
	return sources
}

// reviewCommandDispatchVerbs mechanically extracts every case label inside
// runReviewCommandContext and runReviewCommand in review_facade.go -- the two
// switches RunReview ultimately dispatches every `brunnr-code review <verb>`
// invocation through. It parses the source and reads only the top-level
// `switch args[0]` in each function, so multiline labels and nested switches
// cannot alter the set.
func reviewCommandDispatchVerbs(t *testing.T) map[string]bool {
	t.Helper()
	source, err := os.ReadFile("review_facade.go")
	if err != nil {
		t.Fatal(err)
	}
	return reviewCommandDispatchVerbsFromSource(t, source)
}

func reviewCommandDispatchVerbsFromSource(t *testing.T, source []byte) map[string]bool {
	t.Helper()
	owners, err := reviewCommandDispatchOwners(source)
	if err != nil {
		t.Fatal(err)
	}
	verbs := make(map[string]bool, len(owners))
	for verb := range owners {
		verbs[verb] = true
	}
	return verbs
}

func reviewCommandDispatchOwners(source []byte) (map[string]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "review_facade.go", source, 0)
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	found := map[string]bool{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil || (function.Name.Name != "runReviewCommandContext" && function.Name.Name != "runReviewCommand") {
			continue
		}
		if found[function.Name.Name] {
			return nil, fmt.Errorf("review facade declares %s more than once", function.Name.Name)
		}
		found[function.Name.Name] = true
		dispatch, err := reviewCommandDispatchSwitch(function)
		if err != nil {
			return nil, err
		}
		for _, clause := range dispatch.Body.List {
			caseClause, ok := clause.(*ast.CaseClause)
			if !ok {
				return nil, fmt.Errorf("%s dispatch switch contains %T instead of a case clause", function.Name.Name, clause)
			}
			for _, expression := range caseClause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return nil, fmt.Errorf("%s dispatch case expression = %T, want string literal", function.Name.Name, expression)
				}
				verb, err := strconv.Unquote(literal.Value)
				if err != nil {
					return nil, fmt.Errorf("unquote %s dispatch case: %w", function.Name.Name, err)
				}
				if owner, exists := owners[verb]; exists {
					return nil, fmt.Errorf("review command %q dispatched by both %s and %s", verb, owner, function.Name.Name)
				}
				owners[verb] = function.Name.Name
			}
		}
	}
	for _, name := range []string{"runReviewCommandContext", "runReviewCommand"} {
		if !found[name] {
			return nil, fmt.Errorf("review_facade.go has no %s function", name)
		}
	}
	return owners, nil
}

func reviewCommandDispatchSwitch(function *ast.FuncDecl) (*ast.SwitchStmt, error) {
	var dispatch *ast.SwitchStmt
	for _, statement := range function.Body.List {
		switchStatement, ok := statement.(*ast.SwitchStmt)
		if !ok || !reviewCommandDispatchTag(switchStatement.Tag) {
			continue
		}
		if dispatch != nil {
			return nil, fmt.Errorf("%s has more than one top-level review command dispatch switch", function.Name.Name)
		}
		dispatch = switchStatement
	}
	if dispatch == nil {
		return nil, fmt.Errorf("%s has no top-level `switch args[0]` dispatch", function.Name.Name)
	}
	return dispatch, nil
}

func reviewCommandDispatchTag(expression ast.Expr) bool {
	index, ok := expression.(*ast.IndexExpr)
	if !ok {
		return false
	}
	args, ok := index.X.(*ast.Ident)
	if !ok || args.Name != "args" {
		return false
	}
	literal, ok := index.Index.(*ast.BasicLit)
	return ok && literal.Kind == token.INT && literal.Value == "0"
}

func TestReviewCommandDispatchVerbsReadsMultilineGroupedCases(t *testing.T) {
	const source = `package cli

func runReviewCommandContext(args []string) {
	switch args[0] {
	case "context-first",
		"context-second":
		switch ignored {
		case "nested":
		}
	}
}

func runReviewCommand(args []string) {
	switch args[0] {
	case "command-first",
		"command-second":
	}
}

func unrelated(args []string) {
	switch args[0] {
	case "unrelated":
	}
}
`

	verbs := reviewCommandDispatchVerbsFromSource(t, []byte(source))
	for _, verb := range []string{"context-first", "context-second", "command-first", "command-second"} {
		if !verbs[verb] {
			t.Errorf("multiline grouped dispatch omitted %q: %v", verb, verbs)
		}
	}
	if len(verbs) != 4 || verbs["nested"] || verbs["unrelated"] {
		t.Fatalf("multiline dispatch extraction escaped its two facade switches: %v", verbs)
	}
}

func TestReviewCommandDispatchVerbsRejectsDuplicateFacadeVerb(t *testing.T) {
	const source = `package cli

func runReviewCommandContext(args []string) {
	switch args[0] {
	case "duplicate":
	}
}

func runReviewCommand(args []string) {
	switch args[0] {
	case "duplicate":
	}
}
`

	_, err := reviewCommandDispatchOwners([]byte(source))
	if err == nil || !strings.Contains(err.Error(), `review command "duplicate" dispatched by both runReviewCommandContext and runReviewCommand`) {
		t.Fatalf("duplicate dispatch error = %v", err)
	}
}

// reviewIntegrationNegotiationCallRegexp finds every call site of
// reviewIntegrationNegotiation(flags, *contract) so the dual-mode command set
// below is enumerated from the source, not from memory. It intentionally
// excludes the function definition itself (which does not call itself).
var reviewIntegrationNegotiationCallRegexp = regexp.MustCompile(`negotiated,\s*err\s*:?=\s*reviewIntegrationNegotiation\(`)

var reviewFuncHeaderRegexp = regexp.MustCompile(`^func (\(\w+ \*?\w+\) )?(\w+)\(`)

// reviewIntegrationNegotiationCallSites returns the name of every function
// across internal/cli's production .go files whose body calls
// reviewIntegrationNegotiation(...). This is Guard C's mechanical enumeration
// of "commands with both modes" the task requires.
func reviewIntegrationNegotiationCallSites(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		payload, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		currentFunc := ""
		for _, line := range strings.Split(string(payload), "\n") {
			if match := reviewFuncHeaderRegexp.FindStringSubmatch(line); match != nil {
				currentFunc = match[2]
				continue
			}
			if reviewIntegrationNegotiationCallRegexp.MatchString(line) && currentFunc != "" {
				sites = append(sites, currentFunc)
				currentFunc = ""
			}
		}
	}
	sort.Strings(sites)
	return sites
}

// reviewIntegrationModeCompletenessClassification pins the exact, currently
// exhaustive classification of every dual-mode command's mode-completeness
// evidence. Adding a NEW reviewIntegrationNegotiation call site without
// adding it here fails TestReviewIntegrationDualModeCommandsAreClassified
// closed; removing a call site this map still names does the same.
//
//   - "vacuous": the unnegotiated and negotiated results are the exact same
//     value (proven by encodeReviewIntegrationOperation(stdout, negotiated,
//     <op>, X, X) passing the identical identifier twice), so there is no
//     data gap to hint about.
//   - "explicit-refusal": the negotiated-only data is opt-in behind a flag,
//     and requesting it without --contract is refused with a message naming
//     the exact --contract requirement, instead of silently omitting it.
//   - "hinted": the unnegotiated result can silently omit contract-only data
//     the caller needs (this was the reported defect, scoped to `review
//     start`), so the fix is an additive hint field naming the exact
//     negotiated rerun.
var reviewIntegrationModeCompletenessClassification = map[string]string{
	"runReviewFacadeStart":               "hinted",
	"runReviewFacadeValidateNonDeciding": "vacuous",
}

// TestReviewIntegrationDualModeCommandsAreClassified is Guard C's enumeration
// half: it fails closed in both directions against
// reviewIntegrationModeCompletenessClassification above -- a real dual-mode
// command with no classification, and a classification naming a command that
// no longer branches on negotiation.
func TestReviewIntegrationDualModeCommandsAreClassified(t *testing.T) {
	sites := reviewIntegrationNegotiationCallSites(t)
	if len(sites) == 0 {
		t.Fatal("found no reviewIntegrationNegotiation(...) call sites under internal/cli; the extraction is stale")
	}
	found := map[string]bool{}
	for _, site := range sites {
		found[site] = true
		if _, ok := reviewIntegrationModeCompletenessClassification[site]; !ok {
			t.Errorf("%s calls reviewIntegrationNegotiation but has no mode-completeness classification; classify it in reviewIntegrationModeCompletenessClassification", site)
		}
	}
	for site := range reviewIntegrationModeCompletenessClassification {
		if !found[site] {
			t.Errorf("reviewIntegrationModeCompletenessClassification names %s, but it no longer calls reviewIntegrationNegotiation", site)
		}
	}
}

// reviewIntegrationEncodeOperationCallRegexp extracts the two result
// arguments passed to encodeReviewIntegrationOperation(stdout, negotiated|
// true, <operation constant>, <legacy>, <public>).
var reviewIntegrationEncodeOperationCallRegexp = regexp.MustCompile(`encodeReviewIntegrationOperation\(stdout, (?:negotiated|true), (ReviewIntegrationOperation\w+), (\w+), (\w+)(?:, (?:\*?\w+|""))?\)`)

// TestReviewIntegrationVacuousModeClassificationIsProvenBySource is Guard C's
// evidence half for the "vacuous" classification: it re-derives, from every
// encodeReviewIntegrationOperation(...) call site in review_facade.go,
// whether the legacy and public result arguments are the literal same
// identifier. Any command classified
// "vacuous" above whose call site(s) pass two DIFFERENT identifiers fails
// closed -- the structural proof no longer holds and the classification must
// be revisited (most likely to "hinted" or "explicit-refusal").
func TestReviewIntegrationVacuousModeClassificationIsProvenBySource(t *testing.T) {
	operationToFunc := map[string]string{
		"ReviewIntegrationOperationValidate": "runReviewFacadeValidateNonDeciding",
	}
	vacuousByOperation := map[string]bool{}
	sawOperation := map[string]bool{}
	for _, file := range []string{"review_facade.go"} {
		payload, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range reviewIntegrationEncodeOperationCallRegexp.FindAllStringSubmatch(string(payload), -1) {
			operation, legacy, public := match[1], match[2], match[3]
			sawOperation[operation] = true
			same := legacy == public
			if seen, ok := vacuousByOperation[operation]; ok && seen != same {
				t.Fatalf("%s has inconsistent encodeReviewIntegrationOperation call sites: one passes identical legacy/public arguments and another does not", operation)
			}
			vacuousByOperation[operation] = same
		}
	}
	if len(sawOperation) == 0 {
		t.Fatal("found no encodeReviewIntegrationOperation(...) call sites; the extraction regexp is stale")
	}
	for operation, fn := range operationToFunc {
		if !sawOperation[operation] {
			t.Fatalf("expected %s to be emitted via encodeReviewIntegrationOperation, but no call site was found", operation)
		}
		classification := reviewIntegrationModeCompletenessClassification[fn]
		isVacuous := vacuousByOperation[operation]
		switch classification {
		case "vacuous":
			if !isVacuous {
				t.Errorf("%s is classified %q, but its encodeReviewIntegrationOperation call passes different legacy/public arguments; the negotiated form now carries data the unnegotiated form lacks, so the classification is stale", fn, classification)
			}
		case "explicit-refusal", "hinted":
			if isVacuous {
				t.Errorf("%s is classified %q, but its encodeReviewIntegrationOperation call passes the identical legacy/public argument; there is no longer a data gap to refuse or hint about, so the classification is stale", fn, classification)
			}
		}
	}
}

// TestReviewIntegrationNonVacuousModeClassificationsAreEvidenced is Guard C's
// evidence half for "explicit-refusal" and "hinted": it proves each such
// function's own body actually contains the named guard, rather than trusting
// the classification map's comment.
func TestReviewIntegrationNonVacuousModeClassificationsAreEvidenced(t *testing.T) {
	cases := []struct {
		fn       string
		file     string
		evidence string
	}{
		// Shipped START must route through the compact atomic boundary. Its
		// implementation, checked below, can only create or replay compact
		// authority and cannot reach either legacy or v3 creation.
		{fn: "runReviewFacadeStart", file: "review_facade.go", evidence: "runReviewFacadeCompactAtomicStart(ctx, root, request)"},
	}
	for _, testCase := range cases {
		t.Run(testCase.fn, func(t *testing.T) {
			if reviewIntegrationModeCompletenessClassification[testCase.fn] == "vacuous" {
				t.Fatalf("test case %s is stale: it is now classified vacuous, drop it from this table", testCase.fn)
			}
			payload, err := os.ReadFile(testCase.file)
			if err != nil {
				t.Fatal(err)
			}
			body := reviewExtractFuncBody(t, string(payload), testCase.fn)
			if !strings.Contains(body, testCase.evidence) {
				t.Errorf("%s no longer references %q; its mode-completeness classification is unproven", testCase.fn, testCase.evidence)
			}
			if testCase.fn != "runReviewFacadeStart" {
				return
			}

			atomicPayload, err := os.ReadFile("review_facade_new_lineage.go")
			if err != nil {
				t.Fatal(err)
			}
			atomicBody := reviewExtractFuncBody(t, string(atomicPayload), "runReviewFacadeCompactAtomicStart")
			if !strings.Contains(atomicBody, "store.CreateOrReplayAtomicStart(ctx, request)") {
				t.Error("compact atomic START boundary no longer calls CreateOrReplayAtomicStart")
			}
			for _, creator := range []string{
				"reviewtransaction.AuthoritativeStore(",
				"reviewtransaction.NewLineageAuthorityStore(",
			} {
				if strings.Contains(body, creator) || strings.Contains(atomicBody, creator) {
					t.Errorf("shipped START can reach retired authority creator %q", creator)
				}
			}
		})
	}
}

// reviewExtractFuncBody returns the exact source text of one top-level
// function, matched by brace counting from `func <name>(` (or `func
// (receiver) <name>(`) to its closing brace, so evidence checks only ever see
// that one function's body.
func reviewExtractFuncBody(t *testing.T, source, name string) string {
	t.Helper()
	marker := regexp.MustCompile(`(?m)^func (\(\w+ \*?\w+\) )?` + regexp.QuoteMeta(name) + `\(`)
	loc := marker.FindStringIndex(source)
	if loc == nil {
		t.Fatalf("function %s not found", name)
	}
	rest := source[loc[0]:]
	brace := strings.IndexByte(rest, '{')
	if brace < 0 {
		t.Fatalf("function %s has no body", name)
	}
	depth := 0
	for index := brace; index < len(rest); index++ {
		switch rest[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:index+1]
			}
		}
	}
	t.Fatalf("function %s body never closes", name)
	return ""
}
