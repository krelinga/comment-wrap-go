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
			avail := limit - len(indent) - 3 // 3 == len("// ")
			if avail <= 0 {
				continue // indent alone exceeds limit; leave as-is
			}

			// Join all comment bodies into one string and reflow.
			parts := make([]string, len(para.comments))
			for i, c := range para.comments {
				parts[i] = c.Text[3:] // strip "// "
			}
			wrapped := wrapText(strings.Join(parts, " "), avail)

			// Build replacement covering the full paragraph span.  Every
			// output line (including the first) carries the indent because the
			// edit covers the full source-line range.
			var sb strings.Builder
			for i, wl := range wrapped {
				if i > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(indent)
				sb.WriteString("// ")
				sb.WriteString(wl)
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
type paragraph struct {
	comments []*ast.Comment
	lineIdxs []int
	indent   string
}

// splitParagraphs partitions a CommentGroup's list into paragraphs.
//
// A comment is eligible when:
//   - Its text is "// " followed by a non-whitespace character (exactly one
//     space after //).  This excludes directives (//go:build, //nolint:…),
//     blank comment lines (//), and godoc code blocks (//  indented).
//   - Its source line has only whitespace before // (not an inline comment).
//
// Consecutive eligible comments with the same indent are grouped; any
// ineligible comment resets the current paragraph.
func splitParagraphs(list []*ast.Comment, lines []string, fset *token.FileSet) []paragraph {
	var result []paragraph
	var cur *paragraph

	for _, c := range list {
		text := c.Text

		// Must be "// x" where x is non-whitespace — exactly one space after //.
		if len(text) < 4 || text[2] != ' ' || text[3] == ' ' || text[3] == '\t' {
			cur = nil
			continue
		}

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

		if cur != nil && cur.indent == indent {
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
