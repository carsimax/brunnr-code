package filemerge

import (
	"strings"
	"testing"
)

func TestInjectMarkdownSection_EmptyFile(t *testing.T) {
	result := InjectMarkdownSection("", "sdd", "## SDD Config\nSome content here.\n")

	want := "<!-- gentle-ai:sdd -->\n## SDD Config\nSome content here.\n<!-- /gentle-ai:sdd -->\n"
	if result != want {
		t.Fatalf("empty file inject:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestExtractHTMLCommentSection(t *testing.T) {
	content := "before\n<!-- section:model-small -->\nsmall body\n<!-- /section:model-small -->\nafter\n"
	if got := ExtractHTMLCommentSection(content, "model-small"); got != "small body\n" {
		t.Fatalf("ExtractHTMLCommentSection() = %q, want section body", got)
	}
	if got := ExtractHTMLCommentSection(content, "missing"); got != content {
		t.Fatalf("ExtractHTMLCommentSection() missing = %q, want original content", got)
	}
}

func TestExtractHTMLCommentSection_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "lone start marker returns original content",
			content: "before\n<!-- section:model-small -->\nsmall body\n",
		},
		{
			name:    "lone end marker returns original content",
			content: "before\n<!-- /section:model-small -->\nafter\n",
		},
		{
			name:    "reversed markers return original content",
			content: "<!-- /section:model-small -->\nbody\n<!-- section:model-small -->\n",
		},
		{
			name:    "repeated sections extract the first complete section",
			content: "<!-- section:model-small -->\nfirst\n<!-- /section:model-small -->\n<!-- section:model-small -->\nsecond\n<!-- /section:model-small -->\n",
			want:    "first\n",
		},
		{
			name:    "extra trailing end marker is ignored after first pair",
			content: "<!-- section:model-small -->\nbody\n<!-- /section:model-small -->\n<!-- /section:model-small -->\n",
			want:    "body\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if want == "" {
				want = tt.content
			}
			if got := ExtractHTMLCommentSection(tt.content, "model-small"); got != want {
				t.Fatalf("ExtractHTMLCommentSection() = %q, want %q", got, want)
			}
		})
	}
}

func TestInjectMarkdownSection_AppendToExistingContent(t *testing.T) {
	existing := "# My Config\n\nSome existing content.\n"
	result := InjectMarkdownSection(existing, "persona", "You are a senior architect.\n")

	want := "# My Config\n\nSome existing content.\n\n<!-- gentle-ai:persona -->\nYou are a senior architect.\n<!-- /gentle-ai:persona -->\n"
	if result != want {
		t.Fatalf("append to existing:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestInjectMarkdownSection_UpdateExistingSection(t *testing.T) {
	existing := "# Config\n\n<!-- gentle-ai:sdd -->\nOld SDD content.\n<!-- /gentle-ai:sdd -->\n\nOther stuff.\n"
	result := InjectMarkdownSection(existing, "sdd", "New SDD content.\n")

	want := "# Config\n\n<!-- gentle-ai:sdd -->\nNew SDD content.\n<!-- /gentle-ai:sdd -->\n\nOther stuff.\n"
	if result != want {
		t.Fatalf("update existing section:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestInjectMarkdownSection_CollapsesDuplicateTargetSections(t *testing.T) {
	const (
		open  = "<!-- gentle-ai:persona -->"
		close = "<!-- /gentle-ai:persona -->"
	)

	first := open + "\nfirst\n" + close
	second := open + "\nsecond\n" + close
	third := open + "\nthird\n" + close
	canonical := open + "\ncurrent\n" + close
	sdd := "<!-- gentle-ai:sdd -->\nkeep sdd\n<!-- /gentle-ai:sdd -->"

	tests := []struct {
		name        string
		existing    string
		replacement string
		want        string
	}{
		{
			name:        "adjacent duplicate blocks",
			existing:    first + second,
			replacement: "current\n",
			want:        canonical,
		},
		{
			name:        "user content between duplicate blocks",
			existing:    "# Before\n\n" + first + "\nUser middle.\n\n" + second + "\n# After\n",
			replacement: "current\n",
			want:        "# Before\n\n" + canonical + "\nUser middle.\n\n\n# After\n",
		},
		{
			name:        "three duplicate blocks collapse in one call",
			existing:    first + "\n" + second + "\n" + third,
			replacement: "current\n",
			want:        canonical + "\n\n",
		},
		{
			name:        "unrelated managed section remains unchanged",
			existing:    first + "\n" + sdd + "\n" + second,
			replacement: "current\n",
			want:        canonical + "\n" + sdd + "\n",
		},
		{
			name:        "empty replacement removes every target block",
			existing:    "# Before\n\n" + first + "\nUser middle.\n\n" + second + "\n# After\n",
			replacement: "",
			want:        "# Before\nUser middle.\n\n\n# After\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectMarkdownSection(tt.existing, "persona", tt.replacement)
			if got != tt.want {
				t.Fatalf("InjectMarkdownSection() = %q, want %q", got, tt.want)
			}

			secondRun := InjectMarkdownSection(got, "persona", tt.replacement)
			if secondRun != got {
				t.Fatalf("second injection changed result:\nfirst:  %q\nsecond: %q", got, secondRun)
			}
		})
	}
}

func TestInjectMarkdownSection_MultipleSectionsOnlyTargetedOneUpdated(t *testing.T) {
	existing := "# Config\n\n<!-- gentle-ai:persona -->\nPersona content.\n<!-- /gentle-ai:persona -->\n\n<!-- gentle-ai:sdd -->\nOld SDD.\n<!-- /gentle-ai:sdd -->\n\n<!-- gentle-ai:skills -->\nSkills content.\n<!-- /gentle-ai:skills -->\n"

	result := InjectMarkdownSection(existing, "sdd", "Updated SDD.\n")

	// persona and skills should be unchanged
	want := "# Config\n\n<!-- gentle-ai:persona -->\nPersona content.\n<!-- /gentle-ai:persona -->\n\n<!-- gentle-ai:sdd -->\nUpdated SDD.\n<!-- /gentle-ai:sdd -->\n\n<!-- gentle-ai:skills -->\nSkills content.\n<!-- /gentle-ai:skills -->\n"
	if result != want {
		t.Fatalf("multiple sections:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestInjectMarkdownSection_PreserveUserContentBeforeAndAfter(t *testing.T) {
	existing := "# User's custom intro\n\nHand-written notes.\n\n<!-- gentle-ai:persona -->\nAuto persona.\n<!-- /gentle-ai:persona -->\n\n# User's custom footer\n\nMore hand-written content.\n"

	result := InjectMarkdownSection(existing, "persona", "Updated persona.\n")

	want := "# User's custom intro\n\nHand-written notes.\n\n<!-- gentle-ai:persona -->\nUpdated persona.\n<!-- /gentle-ai:persona -->\n\n# User's custom footer\n\nMore hand-written content.\n"
	if result != want {
		t.Fatalf("preserve user content:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestInjectMarkdownSection_MalformedMarkersTreatedAsNotFound(t *testing.T) {
	// Only opening marker, no closing marker — treat as not found, append.
	existing := "# Config\n\n<!-- gentle-ai:sdd -->\nOrphaned content.\n"
	result := InjectMarkdownSection(existing, "sdd", "New SDD content.\n")

	// Should append since closing marker is missing.
	if result == existing {
		t.Fatalf("malformed markers: expected content to be appended, but got unchanged result")
	}

	// Result should contain the new properly-formed section.
	wantOpen := "<!-- gentle-ai:sdd -->\nNew SDD content.\n<!-- /gentle-ai:sdd -->\n"
	if !strings.Contains(result, wantOpen) {
		t.Fatalf("malformed markers: result should contain proper section:\ngot: %q", result)
	}
}

func TestInjectMarkdownSection_CloseBeforeOpenTreatedAsNotFound(t *testing.T) {
	// Closing marker appears before opening — treat as not found.
	existing := "<!-- /gentle-ai:sdd -->\nSome content.\n<!-- gentle-ai:sdd -->\n"
	result := InjectMarkdownSection(existing, "sdd", "New content.\n")

	// Should append the section, not replace.
	wantSuffix := "<!-- gentle-ai:sdd -->\nNew content.\n<!-- /gentle-ai:sdd -->\n"
	if !strings.HasSuffix(result, wantSuffix) {
		t.Fatalf("close-before-open: expected appended section:\ngot: %q\nwant suffix: %q", result, wantSuffix)
	}
}

// TestInjectMarkdownSection_OrphanRepair covers the four scenarios from issue #301:
// infinite block accumulation caused by orphan closing markers being mishandled.
func TestInjectMarkdownSection_OrphanRepair(t *testing.T) {
	const sid = "engram-protocol"
	open := "<!-- gentle-ai:" + sid + " -->"
	close := "<!-- /gentle-ai:" + sid + " -->"
	newContent := "Engram protocol content.\n"

	oneBlock := open + "\n" + newContent + close + "\n"

	tests := []struct {
		name     string
		existing string
		// wantOnce is the result after the FIRST sync.
		// wantTwice is the result after running sync a SECOND time on wantOnce.
		// Both must equal oneBlock (possibly with surrounding user content).
		checkOnce  func(t *testing.T, result string)
		checkTwice func(t *testing.T, result string)
	}{
		{
			// Scenario 1: clean file with exactly one well-formed block.
			// Sync must replace content in-place — file must not grow.
			name:     "clean file with one block — idempotent replace",
			existing: oneBlock,
			checkOnce: func(t *testing.T, result string) {
				if result != oneBlock {
					t.Fatalf("clean-block: expected unchanged block\ngot:  %q\nwant: %q", result, oneBlock)
				}
			},
			checkTwice: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("clean-block idempotent: expected 1 open marker, got %d\n%q", count, result)
				}
			},
		},
		{
			// Scenario 2: orphan opener (opening marker exists but closing marker
			// was deleted). Sync must repair — not append — resulting in exactly
			// one complete block.
			name:     "orphan opener (no closing marker) — repaired to one block",
			existing: "# Preamble\n\n" + open + "\nOld content.\n",
			checkOnce: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("orphan-opener: expected exactly 1 open marker after repair, got %d\n%q", count, result)
				}
				if !strings.Contains(result, close) {
					t.Fatalf("orphan-opener: result must contain the close marker\n%q", result)
				}
				if !strings.Contains(result, newContent) {
					t.Fatalf("orphan-opener: result must contain new content\n%q", result)
				}
				if !strings.Contains(result, "# Preamble") {
					t.Fatalf("orphan-opener: user preamble must be preserved\n%q", result)
				}
			},
			checkTwice: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("orphan-opener idempotent: expected 1 open marker, got %d\n%q", count, result)
				}
			},
		},
		{
			// Scenario 3: file already has two blocks — one orphan opener from a
			// previous buggy sync run PLUS one full block appended afterwards.
			// A single sync must collapse to one block.
			name: "two blocks (orphan opener + appended block) — collapsed to one",
			existing: "# Preamble\n\n" + open + "\nOld content.\n" +
				"\n" + open + "\n" + newContent + close + "\n",
			checkOnce: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("two-blocks: expected exactly 1 open marker after collapse, got %d\n%q", count, result)
				}
				if !strings.Contains(result, close) {
					t.Fatalf("two-blocks: result must contain the close marker\n%q", result)
				}
			},
			checkTwice: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("two-blocks idempotent: expected 1 open marker, got %d\n%q", count, result)
				}
			},
		},
		{
			// Scenario 4: file has NO markers at all. First sync must add exactly
			// one complete block without duplicating anything.
			name:     "no markers — first sync adds exactly one block",
			existing: "# Clean file\n\nUser content only.\n",
			checkOnce: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("no-markers: expected 1 open marker after first sync, got %d\n%q", count, result)
				}
				if !strings.Contains(result, close) {
					t.Fatalf("no-markers: result must contain the close marker\n%q", result)
				}
				if !strings.Contains(result, "User content only.") {
					t.Fatalf("no-markers: user content must be preserved\n%q", result)
				}
			},
			checkTwice: func(t *testing.T, result string) {
				count := strings.Count(result, open)
				if count != 1 {
					t.Fatalf("no-markers idempotent: expected 1 open marker, got %d\n%q", count, result)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			once := InjectMarkdownSection(tc.existing, sid, newContent)
			tc.checkOnce(t, once)

			twice := InjectMarkdownSection(once, sid, newContent)
			tc.checkTwice(t, twice)
		})
	}
}

func TestInjectMarkdownSection_EmptyContentRemovesSection(t *testing.T) {
	existing := "# Config\n\n<!-- gentle-ai:sdd -->\nSDD content here.\n<!-- /gentle-ai:sdd -->\n\nOther stuff.\n"
	result := InjectMarkdownSection(existing, "sdd", "")

	want := "# Config\n\nOther stuff.\n"
	if result != want {
		t.Fatalf("empty content removes section:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestInjectMarkdownSection_EmptyContentOnMissingSectionNoOp(t *testing.T) {
	existing := "# Config\n\nSome content.\n"
	result := InjectMarkdownSection(existing, "sdd", "")

	if result != existing {
		t.Fatalf("empty content on missing section should be no-op:\ngot:  %q\nwant: %q", result, existing)
	}
}

func TestInjectMarkdownSection_ContentWithoutTrailingNewline(t *testing.T) {
	result := InjectMarkdownSection("", "test", "no trailing newline")

	want := "<!-- gentle-ai:test -->\nno trailing newline\n<!-- /gentle-ai:test -->\n"
	if result != want {
		t.Fatalf("content without trailing newline:\ngot:  %q\nwant: %q", result, want)
	}
}

func TestInjectMarkdownSection_ExistingWithoutTrailingNewline(t *testing.T) {
	existing := "# Title"
	result := InjectMarkdownSection(existing, "test", "Content.\n")

	want := "# Title\n\n<!-- gentle-ai:test -->\nContent.\n<!-- /gentle-ai:test -->\n"
	if result != want {
		t.Fatalf("existing without trailing newline:\ngot:  %q\nwant: %q", result, want)
	}
}

// --- StripLegacyATLBlock tests ---

const legacyATLBlock = `<!-- BEGIN:agent-teams-lite -->
## Agent Teams Orchestrator

You are a COORDINATOR, not an executor.

### Delegation Rules (ALWAYS ACTIVE)

| Rule | Instruction |
|------|------------|
| No inline work | Reading/writing code → delegate to sub-agent |
<!-- END:agent-teams-lite -->`

func TestStripLegacyATLBlock_OnlyATLBlock_ReturnsEmpty(t *testing.T) {
	result := StripLegacyATLBlock(legacyATLBlock)
	if result != "" {
		t.Fatalf("only ATL block: expected empty string, got %q", result)
	}
}

func TestStripLegacyATLBlock_ATLBlockThenMarkers_StripsATLKeepsMarkers(t *testing.T) {
	sddSection := "<!-- gentle-ai:sdd-orchestrator -->\nSome orchestrator content.\n<!-- /gentle-ai:sdd-orchestrator -->\n"
	input := legacyATLBlock + "\n\n" + sddSection

	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("ATL open marker should have been stripped")
	}
	if strings.Contains(result, "<!-- END:agent-teams-lite -->") {
		t.Fatal("ATL close marker should have been stripped")
	}
	if !strings.Contains(result, "<!-- gentle-ai:sdd-orchestrator -->") {
		t.Fatal("sdd-orchestrator marker section must be preserved")
	}
	if !strings.Contains(result, "<!-- /gentle-ai:sdd-orchestrator -->") {
		t.Fatal("sdd-orchestrator close marker must be preserved")
	}
}

func TestStripLegacyATLBlock_ContentBeforeATL_StripsOnlyATL(t *testing.T) {
	before := "# My Config\n\nSome user content.\n"
	sddSection := "<!-- gentle-ai:sdd-orchestrator -->\nOrchestrator stuff.\n<!-- /gentle-ai:sdd-orchestrator -->\n"
	input := before + "\n" + legacyATLBlock + "\n\n" + sddSection

	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("ATL open marker should have been stripped")
	}
	if !strings.Contains(result, "# My Config") {
		t.Fatal("user content before ATL block must be preserved")
	}
	if !strings.Contains(result, "<!-- gentle-ai:sdd-orchestrator -->") {
		t.Fatal("sdd-orchestrator section must be preserved")
	}
}

func TestStripLegacyATLBlock_NoATLBlock_ReturnsUnchanged(t *testing.T) {
	input := "# My Config\n\nSome content without ATL block.\n"
	result := StripLegacyATLBlock(input)
	if result != input {
		t.Fatalf("no ATL block: expected unchanged result:\ngot:  %q\nwant: %q", result, input)
	}
}

func TestStripLegacyATLBlock_OnlyOpenMarkerNoClose_StripsOrphanMarker(t *testing.T) {
	input := "<!-- BEGIN:agent-teams-lite -->\nSome content without close marker.\n"
	result := StripLegacyATLBlock(input)
	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("orphan BEGIN marker should be stripped by post-loop cleanup")
	}
	if !strings.Contains(result, "Some content without close marker.") {
		t.Fatal("content around orphan BEGIN marker should be preserved")
	}
}

func TestStripLegacyATLBlock_ATLBlockAndSDDOrchestrator_StripsOnlyATL(t *testing.T) {
	sddSection := "<!-- gentle-ai:sdd-orchestrator -->\nYou are a COORDINATOR.\n<!-- /gentle-ai:sdd-orchestrator -->\n"
	input := legacyATLBlock + "\n\n" + sddSection

	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("ATL block should have been stripped")
	}
	if !strings.Contains(result, "<!-- gentle-ai:sdd-orchestrator -->") {
		t.Fatal("sdd-orchestrator section must be preserved after ATL strip")
	}
	if !strings.Contains(result, "You are a COORDINATOR.") {
		t.Fatal("sdd-orchestrator content must be preserved")
	}
}

func TestStripLegacyATLBlock_EmptyFile_ReturnsEmpty(t *testing.T) {
	result := StripLegacyATLBlock("")
	if result != "" {
		t.Fatalf("empty file: expected empty result, got %q", result)
	}
}

func TestStripLegacyATLBlock_Idempotent(t *testing.T) {
	// Calling twice should produce the same result as calling once.
	sddSection := "<!-- gentle-ai:sdd-orchestrator -->\nOrchestrator.\n<!-- /gentle-ai:sdd-orchestrator -->\n"
	input := legacyATLBlock + "\n\n" + sddSection

	once := StripLegacyATLBlock(input)
	twice := StripLegacyATLBlock(once)

	if once != twice {
		t.Fatalf("idempotent: second call changed result:\nfirst:  %q\nsecond: %q", once, twice)
	}
}

func TestStripLegacyATLBlock_EmptyBetweenMarkers(t *testing.T) {
	// An ATL block with nothing between the markers should strip to empty.
	input := "<!-- BEGIN:agent-teams-lite -->\n<!-- END:agent-teams-lite -->"
	result := StripLegacyATLBlock(input)
	if result != "" {
		t.Fatalf("empty between markers: expected empty string, got %q", result)
	}
}

func TestStripLegacyATLBlock_DuplicateBlocks(t *testing.T) {
	// A file with two ATL blocks (e.g. pasted twice) — both must be stripped.
	block := "<!-- BEGIN:agent-teams-lite -->\nsome content\n<!-- END:agent-teams-lite -->"
	input := block + "\n\n" + block
	result := StripLegacyATLBlock(input)
	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("duplicate blocks: first ATL open marker should have been stripped")
	}
	if strings.Contains(result, "<!-- END:agent-teams-lite -->") {
		t.Fatal("duplicate blocks: ATL close marker should have been stripped")
	}
	if result != "" {
		t.Fatalf("duplicate blocks: expected empty string after stripping both, got %q", result)
	}
}

func TestStripLegacyATLBlock_EndBeforeBeginWithValidPairAfter(t *testing.T) {
	// A stray END marker appears before a valid BEGIN...END pair.
	// The valid block must still be stripped.
	strayEnd := "<!-- END:agent-teams-lite -->\n"
	validBlock := "<!-- BEGIN:agent-teams-lite -->\nreal content\n<!-- END:agent-teams-lite -->"
	after := "\n\nsome other content"
	input := strayEnd + validBlock + after

	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("end-before-begin: valid ATL open marker should have been stripped")
	}
	if strings.Contains(result, "real content") {
		t.Fatal("end-before-begin: valid ATL block content should have been stripped")
	}
	if !strings.Contains(result, "some other content") {
		t.Fatal("end-before-begin: content after valid ATL block must be preserved")
	}
	if strings.Contains(result, "<!-- END:agent-teams-lite -->") {
		t.Fatal("end-before-begin: orphan END marker should have been removed from output")
	}
}

func TestStripLegacyATLBlock_CRLFLineEndings(t *testing.T) {
	// CRLF line endings should be trimmed cleanly without stray \r characters.
	input := "before\r\n\r\n<!-- BEGIN:agent-teams-lite -->\r\ncontent\r\n<!-- END:agent-teams-lite -->\r\n\r\nafter\r\n"
	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("ATL block should be stripped")
	}
	if !strings.Contains(result, "before") {
		t.Fatal("content before block must be preserved")
	}
	if !strings.Contains(result, "after") {
		t.Fatal("content after block must be preserved")
	}
	// No stray \r should remain at the join point
	if strings.Contains(result, "\r\n\r\n\n") || strings.Contains(result, "\n\r\n\r") {
		t.Fatalf("CRLF: stray carriage returns at join point:\n%q", result)
	}
}

func TestStripLegacyATLBlock_InlineMarkerNotStripped(t *testing.T) {
	// ATL markers appearing inline (not at the start of a line) should NOT be stripped.
	input := "See <!-- BEGIN:agent-teams-lite --> for reference.\nAnd <!-- END:agent-teams-lite --> too.\n"
	result := StripLegacyATLBlock(input)
	if result != input {
		t.Fatalf("inline markers should not be stripped:\ngot:  %q\nwant: %q", result, input)
	}
}

func TestStripLegacyATLBlock_OrphanMarkersCRLF(t *testing.T) {
	// Orphan END marker with CRLF line endings — must be stripped without leaving stray \r.
	input := "before\r\n<!-- END:agent-teams-lite -->\r\nafter"
	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- END:agent-teams-lite -->") {
		t.Fatal("orphan END marker should be stripped")
	}
	if !strings.Contains(result, "before") {
		t.Fatal("content before orphan must be preserved")
	}
	if !strings.Contains(result, "after") {
		t.Fatal("content after orphan must be preserved")
	}
	// No stray \r between "before" and "after" — the marker line should be cleanly removed
	if strings.Contains(result, "\r\n\r\r") || strings.Contains(result, "\r\r") {
		t.Fatalf("orphan CRLF: stray \\r in output:\n%q", result)
	}
}

func TestStripLegacyATLBlock_OrphanBeginCRLF(t *testing.T) {
	// Orphan BEGIN marker with CRLF — must be stripped without stray \r.
	input := "before\r\n<!-- BEGIN:agent-teams-lite -->\r\nsome content\r\n"
	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("orphan BEGIN marker should be stripped")
	}
	if !strings.Contains(result, "before") {
		t.Fatal("content before orphan must be preserved")
	}
	if !strings.Contains(result, "some content") {
		t.Fatal("content after orphan BEGIN must be preserved")
	}
}

func TestStripLegacyATLBlock_MultiBlocksWithContentBetween(t *testing.T) {
	// Two ATL blocks with user content between them — both blocks stripped,
	// user content preserved.
	block := "<!-- BEGIN:agent-teams-lite -->\nATL stuff\n<!-- END:agent-teams-lite -->"
	input := block + "\n\nuser content here\n\n" + block
	result := StripLegacyATLBlock(input)

	if strings.Contains(result, "<!-- BEGIN:agent-teams-lite -->") {
		t.Fatal("both ATL blocks should be stripped")
	}
	if strings.Contains(result, "ATL stuff") {
		t.Fatal("ATL content should be stripped")
	}
	if !strings.Contains(result, "user content here") {
		t.Fatal("user content between blocks must be preserved")
	}
}
