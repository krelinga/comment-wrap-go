// Package wrap implements line-length-based wrapping of // comment lines in Go
// source files.
package wrap

import (
	"bytes"
	"go/parser"
	"go/token"
	"strings"
)

// File parses src as a Go source file and returns a copy where every eligible
// // comment line that exceeds limit characters has been word-wrapped.
//
// A comment line is eligible when:
//  1. The // marker is the first non-whitespace on its source line (i.e. it is
//     not an inline comment after code).
//  2. The // marker is immediately followed by a space or tab (so directives
//     such as //go:generate, //nolint:, //go:build are left untouched).
//
// Words are never split mid-word.  If a single word is longer than the
// available width it is placed on its own line.  Lines containing URLs that
// would have to be split are kept intact (the URL word is never broken).
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
		for _, c := range cg.List {
			text := c.Text // e.g. "// some words here"

			// Only handle // comments.
			if !strings.HasPrefix(text, "//") {
				continue
			}

			// Must be immediately followed by space/tab (skip directives).
			if len(text) < 3 || (text[2] != ' ' && text[2] != '\t') {
				continue
			}

			// Locate the source line that contains this comment.
			pos := fset.Position(c.Pos())
			lineIdx := pos.Line - 1 // 0-based
			if lineIdx < 0 || lineIdx >= len(lines) {
				continue
			}
			line := lines[lineIdx]

			// Skip inline comments: something non-whitespace must appear
			// before // on this line.
			slashPos := strings.Index(line, "//")
			if slashPos < 0 {
				continue
			}
			prefix := line[:slashPos]
			if strings.TrimSpace(prefix) != "" {
				continue // inline comment after code
			}

			// Check length against limit (use the raw source line).
			if len(line) <= limit {
				continue
			}

			indent := prefix // only whitespace
			// The comment body is everything after "// " (or "//\t").
			body := text[3:]

			// Available width for text on each continuation line is:
			//   limit - len(indent) - len("// ")
			avail := limit - len(indent) - 3
			if avail <= 0 {
				continue // indent alone exceeds limit; leave it alone
			}

			wrapped := wrapText(body, avail)
			if len(wrapped) <= 1 {
				// wrapText returned a single line — nothing useful to do.
				continue
			}

			// Build the replacement string.  Every line (including the first)
			// needs the indent because the edit covers the full source line.
			var sb strings.Builder
			for i, wl := range wrapped {
				if i > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(indent)
				sb.WriteString("// ")
				sb.WriteString(wl)
			}

			// Calculate byte offsets for this line in src.
			startOff := lineStart(src, lineIdx)
			endOff := startOff + len(line)

			edits = append(edits, edit{
				start: startOff,
				end:   endOff,
				text:  sb.String(),
			})
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
