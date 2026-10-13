package assets

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// The agents choose a skill from its frontmatter description alone, so a
// description that no realistic request matches never loads, and two
// descriptions that say the same thing make the choice a coin flip. These
// tests approximate that choice with a deterministic lexical score (TF-IDF
// cosine) so both failures show up in `go test` instead of in a session.

// skillTriggerCases maps every embedded skill to realistic user requests that
// must route to it. A new skill without an entry fails the test on purpose.
var skillTriggerCases = map[string][]string{
	"branch-pr": {
		"Open a pull request for this branch so it is ready for review.",
		"Prepare the PR and check that it links an issue first.",
	},
	"chained-pr": {
		"This change is over 1200 lines, split it into stacked PRs.",
		"Break the oversized diff into review slices.",
	},
	"cognitive-doc-design": {
		"Rewrite the README so onboarding is easier for new contributors.",
		"Write an RFC that explains the architecture of the install pipeline.",
	},
	"comment-writer": {
		"Draft a reply to this GitHub comment on my PR.",
		"Write a Slack message that answers the reviewer's feedback.",
	},
	"debugging": {
		"This test is flaky and sometimes panics, find the root cause.",
		"A build error started after the last merge, it looks like a regression.",
	},
	"go-testing": {
		"Add Go tests to raise go test coverage for the planner.",
		"Update the golden files for the Bubbletea view using teatest.",
	},
	"issue-creation": {
		"Create an issue for this feature request.",
		"Open a GitHub issue from what we found in the repository and wait for approval.",
	},
	"judgment-day": {
		"Run judgment day on this change.",
		"Do an adversarial dual review of the diff.",
	},
	"rdd-defect-workflow": {
		"The receipt-driven review is stuck on a lineage correction.",
		"The delivery gate kill switch blocked my push, what now?",
	},
	"skill-creator": {
		"Create a new skill that documents how we use AI agents here.",
		"Write agent instructions for this new usage pattern.",
	},
	"skill-improver": {
		"Audit our existing skills and improve their quality.",
		"This skill grew too long and repeats itself, refactor it.",
	},
	"skill-registry": {
		"I added two skills, update the skill registry.",
		"Index the available skills so the paths are current.",
	},
	"systemic-issue-triage": {
		"We have a flood of community reports in the backlog, triage them.",
		"A user is blocked at a dead-end; find the root class instead of patching it.",
	},
	"work-unit-commits": {
		"Split this implementation into commits that keep tests and docs with the code.",
		"Cut a release tag after the commits land.",
	},
}

// maxDescriptionSimilarity is the near-duplicate ceiling for any two skill
// descriptions under the TF-IDF cosine below. Distinct skills peaked at 0.224
// (skill-creator / skill-improver) when this was set, while paraphrased copies
// of chained-pr, judgment-day, and go-testing scored 0.29, 0.51, and 0.59.
const maxDescriptionSimilarity = 0.25

func TestSkillDescriptionsTriggerTheirSkill(t *testing.T) {
	descriptions := embeddedSkillDescriptions(t)
	index := newTriggerIndex(descriptions)

	for name := range descriptions {
		if len(skillTriggerCases[name]) == 0 {
			t.Errorf("skill %q has no routing case in skillTriggerCases; add 1-2 realistic requests that should load it", name)
		}
	}
	for name := range skillTriggerCases {
		if _, ok := descriptions[name]; !ok {
			t.Errorf("skillTriggerCases names %q, which is not an embedded skill", name)
		}
	}

	minMargin, minMarginPrompt := math.Inf(1), ""
	for _, want := range sortedKeys(skillTriggerCases) {
		for _, prompt := range skillTriggerCases[want] {
			ranked := index.rank(prompt)
			wantScore := index.score(prompt, want)
			best := ranked[0]
			if best.name != want {
				t.Errorf("prompt %q: expected skill %q (score %.3f), but %q wins (score %.3f)", prompt, want, wantScore, best.name, best.score)
				continue
			}
			if margin := best.score - ranked[1].score; margin < minMargin {
				minMargin = margin
				minMarginPrompt = fmt.Sprintf("%q -> %s %.3f over %s %.3f", prompt, want, best.score, ranked[1].name, ranked[1].score)
			}
		}
	}
	t.Logf("lowest routing margin %.3f: %s", minMargin, minMarginPrompt)
}

func TestSkillDescriptionsAreNotNearDuplicates(t *testing.T) {
	descriptions := embeddedSkillDescriptions(t)
	index := newTriggerIndex(descriptions)

	type pair struct {
		a, b  string
		score float64
	}
	var pairs []pair
	names := sortedKeys(descriptions)
	for i, a := range names {
		for _, b := range names[i+1:] {
			pairs = append(pairs, pair{a, b, cosine(index.docs[a], index.docs[b])})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })

	for i, p := range pairs {
		if i < 5 {
			t.Logf("similarity %.3f: %s / %s", p.score, p.a, p.b)
		}
		if p.score > maxDescriptionSimilarity {
			t.Errorf("descriptions of %q and %q are near-duplicates: similarity %.3f > %.3f\n  %s: %s\n  %s: %s",
				p.a, p.b, p.score, maxDescriptionSimilarity, p.a, descriptions[p.a], p.b, descriptions[p.b])
		}
	}
}

// embeddedSkillDescriptions returns skill name -> frontmatter description for
// every embedded SKILL.md.
func embeddedSkillDescriptions(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, path := range embeddedSkillPaths(t) {
		fm, err := extractSkillFrontmatter(MustRead(path))
		if err != nil {
			t.Fatalf("%s: extract frontmatter: %v", path, err)
		}
		out[fm.name] = fm.description
	}
	if len(out) < 2 {
		t.Fatalf("need at least two embedded skills to compare, got %d", len(out))
	}
	return out
}

type triggerIndex struct {
	idf  map[string]float64
	docs map[string]map[string]float64
}

type rankedSkill struct {
	name  string
	score float64
}

// newTriggerIndex builds TF-IDF vectors with idf = ln(N/df), so a word every
// description shares (such as "trigger") carries no weight.
func newTriggerIndex(descriptions map[string]string) triggerIndex {
	df := map[string]int{}
	for _, d := range descriptions {
		for term := range termCounts(d) {
			df[term]++
		}
	}
	idx := triggerIndex{idf: map[string]float64{}, docs: map[string]map[string]float64{}}
	n := float64(len(descriptions))
	for term, count := range df {
		idx.idf[term] = math.Log(n / float64(count))
	}
	for name, d := range descriptions {
		idx.docs[name] = idx.vector(d)
	}
	return idx
}

// vector weights each term by its count times the description-corpus idf.
// Terms that no description uses are dropped: they cannot pick a skill.
func (idx triggerIndex) vector(text string) map[string]float64 {
	v := map[string]float64{}
	for term, count := range termCounts(text) {
		if w := idx.idf[term]; w > 0 {
			v[term] = (1 + math.Log(float64(count))) * w
		}
	}
	return v
}

func (idx triggerIndex) score(prompt, name string) float64 {
	return cosine(idx.vector(prompt), idx.docs[name])
}

// rank orders skills by score, breaking ties by name for determinism.
func (idx triggerIndex) rank(prompt string) []rankedSkill {
	q := idx.vector(prompt)
	var out []rankedSkill
	for name, doc := range idx.docs {
		out = append(out, rankedSkill{name, cosine(q, doc)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	return out
}

func cosine(a, b map[string]float64) float64 {
	var dot, na, nb float64
	for term, w := range a {
		dot += w * b[term]
		na += w * w
	}
	for _, w := range b {
		nb += w * w
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

var triggerStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"do": true, "for": true, "from": true, "how": true, "i": true, "in": true, "into": true, "is": true,
	"it": true, "its": true, "my": true, "never": true, "not": true, "of": true, "on": true, "or": true,
	"our": true, "so": true, "that": true, "the": true, "their": true, "them": true, "this": true,
	"to": true, "two": true, "we": true, "what": true, "with": true,
}

// termCounts lowercases, splits on anything that is not a letter or digit,
// drops stopwords, and folds a plural "s" so "tests" matches "test".
func termCounts(text string) map[string]int {
	counts := map[string]int{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		if triggerStopwords[w] {
			continue
		}
		if len(w) > 2 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") {
			w = strings.TrimSuffix(w, "s")
		}
		counts[w]++
	}
	return counts
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
