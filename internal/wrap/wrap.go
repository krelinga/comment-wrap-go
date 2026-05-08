// Package wrap implements line-length-based wrapping of // comment lines in Go
// source files.
package wrap

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// File parses src as a Go source file and returns a copy where every eligible
// // comment paragraph that contains at least one line exceeding limit
// characters is reflowed as a unit.
//
// A comment line is eligible when:
//  1. The // marker is the first non-whitespace on its source line (i.e. it is
//     not an inline comment after code).
//  2. The // marker is immediately followed by exactly one space and then a
//     non-whitespace character, so directives (//go:generate, //nolint:,
//     //go:build) and indented content (godoc code blocks starting with //  )
//     are left untouched.
//
// Consecutive eligible lines with the same source-line indent form a
// paragraph and are reflowed together.  An empty comment line (//), a
// directive, or any other ineligible line breaks the current paragraph.
//
// Words are never split mid-word.  A word longer than the available width is
// placed on its own line without splitting.
//
// The returned slice is identical to src when no changes are needed.
func File(src []byte, limit int) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	type edit struct {
		start, end int // byte offsets into src
		text       string
	}
	var edits []edit

	lines := srcLines(src)

	for _, cg := range f.Comments {
		for _, para := range splitParagraphs(cg.List, lines, fset) {
			// Only reflow if at least one line in the paragraph exceeds the limit.
			anyOver := false
			for _, li := range para.lineIdxs {
				if len(lines[li]) > limit {
					anyOver = true
					break
				}
			}
			if !anyOver {
				continue
			}

			indent := para.indent

			var wrapped []string
			var sb strings.Builder

			if para.isList {
				// List item: first line has the marker; continuation lines are
				// indented to the same column as the text start.
				marker := para.markerStr
				continuationPad := strings.Repeat(" ", len(marker))
				avail := limit - len(indent) - 2 - len(marker) // 2 == len("//")
				if avail <= 0 {
					continue
				}

				// Collect body text from each line, stripping the marker/padding.
				parts := make([]string, len(para.comments))
				for i, c := range para.comments {
					parts[i] = c.Text[2+len(marker):] // strip "//" + marker/continuationPad
				}
				wrapped = wrapText(strings.Join(parts, " "), avail)

				for i, wl := range wrapped {
					if i > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(indent)
					sb.WriteString("//")
					if i == 0 {
						sb.WriteString(marker)
					} else {
						sb.WriteString(continuationPad)
					}
					sb.WriteString(wl)
				}
			} else {
				avail := limit - len(indent) - 3 // 3 == len("// ")
				if avail <= 0 {
					continue // indent alone exceeds limit; leave as-is
				}

				// Join all comment bodies into one string and reflow.
				parts := make([]string, len(para.comments))
				for i, c := range para.comments {
					parts[i] = c.Text[3:] // strip "// "
				}
				wrapped = wrapText(strings.Join(parts, " "), avail)

				// Build replacement covering the full paragraph span.  Every
				// output line (including the first) carries the indent because the
				// edit covers the full source-line range.
				for i, wl := range wrapped {
					if i > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(indent)
					sb.WriteString("// ")
					sb.WriteString(wl)
				}
			}

			firstLI := para.lineIdxs[0]
			lastLI := para.lineIdxs[len(para.lineIdxs)-1]
			startOff := lineStart(src, firstLI)
			endOff := lineStart(src, lastLI) + len(lines[lastLI])

			edits = append(edits, edit{start: startOff, end: endOff, text: sb.String()})
		}
	}

	if len(edits) == 0 {
		return src, nil
	}

	// Apply edits in reverse order to preserve earlier byte offsets.
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}

	out := make([]byte, len(src))
	copy(out, src)
	for _, e := range edits {
		out = applyEdit(out, e.start, e.end, e.text)
	}
	return out, nil
}

// paragraph is a sequence of consecutive eligible // comment lines that share
// the same source-line indent.  It is the unit of reflow.
//
// For list items (isList == true), markerStr holds the text between "//" and
// the item body on the first line, e.g. "   - " or "  1. ".  Continuation
// lines of the same item use the same number of spaces (no marker).
type paragraph struct {
	comments  []*ast.Comment
	lineIdxs  []int
	indent    string
	isList    bool
	markerStr string // non-empty only when isList == true
}

// splitParagraphs partitions a CommentGroup's list into paragraphs.
//
// Plain paragraphs require "// " (one space) followed by a non-whitespace
// character on a pure comment line; consecutive such lines with the same
// source-line indent are grouped.
//
// List-item paragraphs are recognised by listItemMarker.  Each item starts a
// fresh paragraph; continuation lines (same indent depth, no marker) are
// appended to it.  Any ineligible line ends the current paragraph.
func splitParagraphs(list []*ast.Comment, lines []string, fset *token.FileSet) []paragraph {
	var result []paragraph
	var cur *paragraph

	for _, c := range list {
		text := c.Text

		// Resolve source-line indent first — needed for both paths.
		pos := fset.Position(c.Pos())
		lineIdx := pos.Line - 1 // 0-based
		if lineIdx < 0 || lineIdx >= len(lines) {
			cur = nil
			continue
		}
		line := lines[lineIdx]

		// Skip inline comments (non-whitespace before // on the same line).
		slashPos := strings.Index(line, "//")
		if slashPos < 0 || strings.TrimSpace(line[:slashPos]) != "" {
			cur = nil
			continue
		}
		indent := line[:slashPos]

		// --- List item path ---
		if markerStr, ok := listItemMarker(text); ok {
			// Each list item always starts a new paragraph.
			result = append(result, paragraph{
				comments:  []*ast.Comment{c},
				lineIdxs:  []int{lineIdx},
				indent:    indent,
				isList:    true,
				markerStr: markerStr,
			})
			cur = &result[len(result)-1]
			continue
		}

		if cur != nil && cur.isList && isListContinuation(text, cur.markerStr) {
			cur.comments = append(cur.comments, c)
			cur.lineIdxs = append(cur.lineIdxs, lineIdx)
			continue
		}

		// --- Plain paragraph path ---

		// Must be "// x" where x is non-whitespace — exactly one space after //.
		if len(text) < 4 || text[2] != ' ' || text[3] == ' ' || text[3] == '\t' {
			cur = nil
			continue
		}

		if cur != nil && !cur.isList && cur.indent == indent {
			cur.comments = append(cur.comments, c)
			cur.lineIdxs = append(cur.lineIdxs, lineIdx)
		} else {
			result = append(result, paragraph{
				comments: []*ast.Comment{c},
				lineIdxs: []int{lineIdx},
				indent:   indent,
			})
			cur = &result[len(result)-1]
		}
	}

	return result
}

// listItemMarker reports whether text (a full comment text like "//   - foo")
// begins with a Go doc comment list marker.  If so, it returns the marker
// string — everything in text after "//" up to and including the space/tab
// that follows the marker character(s), e.g. "   - " or "  1. ".
func listItemMarker(text string) (markerStr string, ok bool) {
	if len(text) < 2 || text[:2] != "//" {
		return "", false
	}
	rest := text[2:] // everything after "//"

	// Count leading spaces (at least one required to distinguish from a plain
	// paragraph line which has exactly one space).
	i := 0
	for i < len(rest) && rest[i] == ' ' {
		i++
	}
	if i < 1 || i >= len(rest) {
		return "", false
	}

	// Bullet marker: one of - * + •
	ch := rest[i]
	if ch == '-' || ch == '*' || ch == '+' || ch == '\xe2' {
		// Handle Unicode bullet • (U+2022, UTF-8: 0xE2 0x80 0xA2)
		markerEnd := i + 1
		if ch == '\xe2' {
			if len(rest) < i+3 || rest[i+1] != '\x80' || rest[i+2] != '\xa2' {
				return "", false
			}
			markerEnd = i + 3
		}
		if markerEnd >= len(rest) || (rest[markerEnd] != ' ' && rest[markerEnd] != '\t') {
			return "", false
		}
		return rest[:markerEnd+1], true
	}

	// Numbered marker: one or more ASCII digits followed by '.' or ')'
	if ch >= '0' && ch <= '9' {
		j := i
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j >= len(rest) {
			return "", false
		}
		punct := rest[j]
		if punct != '.' && punct != ')' {
			return "", false
		}
		markerEnd := j + 1
		if markerEnd >= len(rest) || (rest[markerEnd] != ' ' && rest[markerEnd] != '\t') {
			return "", false
		}
		return rest[:markerEnd+1], true
	}

	return "", false
}

// isListContinuation reports whether text is a continuation line of a list
// item whose marker string is markerStr.  A continuation line has the same
// number of characters as markerStr (all spaces) after "//", followed by
// non-whitespace content, and is not itself a list marker.
func isListContinuation(text, markerStr string) bool {
	if len(text) < 2+len(markerStr)+1 {
		return false
	}
	if text[:2] != "//" {
		return false
	}
	prefix := text[2 : 2+len(markerStr)]
	for _, b := range []byte(prefix) {
		if b != ' ' && b != '\t' {
			return false
		}
	}
	// The character immediately after the prefix must be non-whitespace
	// (otherwise it could be a blank comment or deeper-indented code block).
	next := text[2+len(markerStr)]
	if next == ' ' || next == '\t' {
		return false
	}
	// Must not itself be a new list item.
	_, isItem := listItemMarker(text)
	return !isItem
}

// wrapText wraps text into lines of at most width runes, splitting only at
// whitespace boundaries.  Words longer than width are placed on their own line
// without splitting.
func wrapText(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	var lines []string
	current := words[0]
	for _, w := range words[1:] {
		if len(current)+1+len(w) <= width {
			current += " " + w
		} else {
			lines = append(lines, current)
			current = w
		}
	}
	lines = append(lines, current)
	return lines
}

// srcLines splits src into lines, stripping trailing \n (but keeping \r if
// present so the lengths remain accurate for limit checking).
func srcLines(src []byte) []string {
	raw := strings.Split(string(src), "\n")
	// The last element after Split is "" when src ends with \n; that is fine.
	return raw
}

// lineStart returns the byte offset in src of the beginning of line lineIdx
// (0-based).
func lineStart(src []byte, lineIdx int) int {
	off := 0
	for i := 0; i < lineIdx; i++ {
		next := bytes.IndexByte(src[off:], '\n')
		if next < 0 {
			return off
		}
		off += next + 1
	}
	return off
}

// applyEdit replaces src[start:end] with replacement and returns the new
// slice.
func applyEdit(src []byte, start, end int, replacement string) []byte {
	rep := []byte(replacement)
	result := make([]byte, 0, len(src)-(end-start)+len(rep))
	result = append(result, src[:start]...)
	result = append(result, rep...)
	result = append(result, src[end:]...)
	return result
}
