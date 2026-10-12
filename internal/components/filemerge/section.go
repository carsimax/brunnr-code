package filemerge

import (
	"strings"
)

// ExtractHTMLCommentSection extracts the content between a paired
// <!-- section:NAME --> ... <!-- /section:NAME --> marker pair. A missing,
// lone, or reversed marker pair returns the full content unchanged.
func ExtractHTMLCommentSection(content, name string) string {
	openMarker := "<!-- section:" + name + " -->"
	closeMarker := "<!-- /section:" + name + " -->"
	start := strings.Index(content, openMarker)
	end := strings.Index(content, closeMarker)
	if start == -1 || end == -1 || end <= start {
		return content
	}
	afterOpen := start + len(openMarker)
	return strings.TrimLeft(content[afterOpen:end], " \t\r\n")
}

const (
	markerPrefix = "<!-- brunnr-code:"
	markerSuffix = " -->"
	closePrefix  = "<!-- /brunnr-code:"
)

// openMarker returns the opening marker for a section ID.
func openMarker(sectionID string) string {
	return markerPrefix + sectionID + markerSuffix
}

// closeMarker returns the closing marker for a section ID.
func closeMarker(sectionID string) string {
	return closePrefix + sectionID + markerSuffix
}

// stripOrphanMarkers removes unpaired opening or closing markers for the given
// sectionID from content before injection logic runs.
//
// An orphan closer is a closing marker that appears with no preceding opening
// marker, OR that appears BEFORE the opening marker in the file. Without this
// cleanup the main injection loop would fall through to append mode, producing
// a duplicate block on every sync call (the root cause of issue #301).
//
// An orphan opener is an opening marker with no subsequent closing marker; it
// is also stripped so the caller can safely append a fresh, well-formed block.
//
// All occurrences of orphan markers are removed — not just the first — so that
// files already corrupted with 20+ duplicate blocks are fully collapsed to a
// clean state after a single sync run.
func stripOrphanMarkers(content, open, close string) string {
	for {
		openIdx := strings.Index(content, open)
		closeIdx := strings.Index(content, close)

		switch {
		case openIdx < 0 && closeIdx < 0:
			// Neither marker present — nothing to strip.
			return content

		case openIdx < 0 && closeIdx >= 0:
			// Orphan closer: closing marker exists but no opener before it.
			// Remove this closer and loop to catch any remaining orphans.
			content = content[:closeIdx] + content[closeIdx+len(close):]

		case openIdx >= 0 && closeIdx < 0:
			// Orphan opener: opening marker exists but no closer follows.
			// Remove the orphan opener so the caller appends a fresh block.
			content = content[:openIdx] + content[openIdx+len(open):]

		case closeIdx < openIdx:
			// Closer appears before opener — the closer is an orphan.
			// Remove it and loop; the opener may then form a valid pair or
			// become an orphan opener that gets cleaned up in the next iteration.
			content = content[:closeIdx] + content[closeIdx+len(close):]

		default:
			// Both markers present and opener precedes closer — valid pair found.
			// Stop; the main injection logic will handle the replacement.
			return content
		}
	}
}

// InjectMarkdownSection replaces or appends a marked section in a markdown file.
// Markers use HTML comments: <!-- brunnr-code:SECTION_ID --> ... <!-- /brunnr-code:SECTION_ID -->
// If the section already exists, its content is replaced.
// If it doesn't exist, it's appended at the end.
// Content outside markers is never touched.
// If content is empty, the section (including markers) is removed.
//
// Before injection, orphan markers (an unpaired closer or opener) are stripped
// so that a file corrupted by a previous buggy sync run is repaired in place
// rather than having another duplicate block appended.
func InjectMarkdownSection(existing, sectionID, content string) string {
	open := openMarker(sectionID)
	close := closeMarker(sectionID)

	// Repair any orphan markers left by previous (buggy) sync runs before
	// attempting replacement. This is the core fix for issue #301.
	existing = stripOrphanMarkers(existing, open, close)

	openIdx := strings.Index(existing, open)
	closeIdx := strings.Index(existing, close)

	// If both markers are found and in the correct order, replace the section.
	if openIdx >= 0 && closeIdx >= 0 && closeIdx > openIdx {
		before := existing[:openIdx]
		after := existing[closeIdx+len(close):]

		var preservedAfter strings.Builder
		for {
			duplicateOpen := strings.Index(after, open)
			if duplicateOpen < 0 {
				preservedAfter.WriteString(after)
				break
			}

			bodyStart := duplicateOpen + len(open)
			duplicateCloseOffset := strings.Index(after[bodyStart:], close)
			if duplicateCloseOffset < 0 {
				preservedAfter.WriteString(after)
				break
			}

			duplicateEnd := bodyStart + duplicateCloseOffset + len(close)
			preservedAfter.WriteString(after[:duplicateOpen])
			after = after[duplicateEnd:]
		}
		after = preservedAfter.String()

		// If content is empty, remove the entire section including markers.
		if content == "" {
			// Clean up trailing newline after close marker.
			if len(after) > 0 && after[0] == '\n' {
				after = after[1:]
			}
			// Clean up trailing newline before open marker.
			result := strings.TrimRight(before, "\n")
			if after != "" {
				if result != "" {
					result += "\n"
				}
				result += after
			} else if result != "" {
				result += "\n"
			}
			return result
		}

		var sb strings.Builder
		sb.WriteString(before)
		sb.WriteString(open)
		sb.WriteString("\n")
		sb.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString(close)
		sb.WriteString(after)
		return sb.String()
	}

	// If content is empty and section doesn't exist, return existing unchanged.
	if content == "" {
		return existing
	}

	// Section not found — append at end.
	var sb strings.Builder
	sb.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		sb.WriteString("\n")
	}
	if existing != "" {
		sb.WriteString("\n")
	}
	sb.WriteString(open)
	sb.WriteString("\n")
	sb.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString(close)
	sb.WriteString("\n")
	return sb.String()
}
