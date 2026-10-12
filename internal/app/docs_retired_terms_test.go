package app_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// retiredDocTerms are workflow terms retired in v4.0.0. Living docs must not
// present them as current guidance. "Strict TDD" and "shadow" stay allowed:
// the first is current guidance and the second is ambiguous.
var retiredDocTerms = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"/sdd-", regexp.MustCompile(regexp.QuoteMeta("/sdd-"))},
	{"/gentle-sdd-", regexp.MustCompile(regexp.QuoteMeta("/gentle-sdd-"))},
	{"brunnr-code sdd-", regexp.MustCompile(regexp.QuoteMeta("brunnr-code sdd-"))},
	{"SDD phases", regexp.MustCompile(regexp.QuoteMeta("SDD phases"))},
	{"SDD agents", regexp.MustCompile(regexp.QuoteMeta("SDD agents"))},
	{"OpenSpec", regexp.MustCompile(regexp.QuoteMeta("OpenSpec"))},
	{"--strict-tdd", regexp.MustCompile(regexp.QuoteMeta("--strict-tdd"))},
	{"SDD", regexp.MustCompile(`\bSDD\b`)},
}

// retirementNote is the whole allowlist: a line may name a retired term only
// when the same line says it is retired or legacy, such as the "retired in
// v4.0.0" notes and the legacy sdd-* telemetry schema values.
var retirementNote = regexp.MustCompile(`(?i)\b(retired|legacy)\b`)

func TestLivingDocsDoNotAdvertiseRetiredTerms(t *testing.T) {
	const name = "README.md"
	content, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	for index, line := range strings.Split(string(content), "\n") {
		if retirementNote.MatchString(line) {
			continue
		}
		for _, term := range retiredDocTerms {
			if term.pattern.MatchString(line) {
				t.Errorf("%s:%d advertises retired term %q", name, index+1, term.name)
			}
		}
	}
}
