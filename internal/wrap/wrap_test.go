package wrap_test

import (
	"strings"
	"testing"

	"github.com/krelinga/comment-wrap-go/internal/wrap"
)

// helper calls wrap.File and returns the string output, failing on error.
func mustWrap(t *testing.T, src string, limit int) string {
	t.Helper()
	out, err := wrap.File([]byte(src), limit)
	if err != nil {
		t.Fatalf("wrap.File error: %v", err)
	}
	return string(out)
}

// TestNoChange verifies that a file with no over-limit comments is returned
// byte-for-byte identical.
func TestNoChange(t *testing.T) {
	src := `package p

// This comment is short.
func Foo() {}
`
	out := mustWrap(t, src, 100)
	if out != src {
		t.Errorf("expected identical output\ngot: %q", out)
	}
}

// TestExactlyAtLimit verifies that a line equal to the limit is not wrapped.
func TestExactlyAtLimit(t *testing.T) {
	// Build a comment line that is exactly `limit` chars long.
	limit := 60
	// "// " + body, indented by nothing.
	// We need len("// "+body) == limit  => len(body) == limit-3 == 57
	body := strings.Repeat("x", limit-3)
	src := "package p\n\n// " + body + "\nfunc Foo() {}\n"
	if len(strings.Split(src, "\n")[2]) != limit {
		t.Fatalf("test setup: line length %d != limit %d", len(strings.Split(src, "\n")[2]), limit)
	}
	out := mustWrap(t, src, limit)
	if out != src {
		t.Errorf("expected no change for line at exactly the limit")
	}
}

// TestOneCharOver verifies that a line one character over the limit is split.
func TestOneCharOver(t *testing.T) {
	src := `package p

// The quick brown fox jumps over the lazy dog and then some more words here now.
func Foo() {}
`
	// limit 60: "// The quick brown fox jumps over the lazy dog and then" is 55 chars,
	// adding " some" would give 60, adding " more" would be 65 — so split there.
	out := mustWrap(t, src, 60)
	lines := strings.Split(out, "\n")
	// Every comment line should be <= 60 chars.
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "// ") && len(l) > 60 {
			t.Errorf("line still over limit (%d chars): %q", len(l), l)
		}
	}
	// The original single comment line should now be multiple lines.
	commentLines := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "// ") {
			commentLines++
		}
	}
	if commentLines < 2 {
		t.Errorf("expected comment to be split into multiple lines, got %d comment lines\noutput:\n%s", commentLines, out)
	}
}

// TestInlineCommentSkipped verifies that inline comments after code are not
// wrapped.
func TestInlineCommentSkipped(t *testing.T) {
	src := `package p

func Foo() {
	x := 1 // this is a very long inline comment that exceeds the limit for sure yes indeed it does
}
`
	out := mustWrap(t, src, 60)
	if out != src {
		t.Errorf("expected inline comment to be left unchanged\ngot:\n%s", out)
	}
}

// TestDirectiveSkipped verifies that //nolint:, //go:generate, //go:build etc.
// are not wrapped (they have no space after //).
func TestDirectiveSkipped(t *testing.T) {
	src := `package p

//go:generate some very long command that exceeds the line length limit by quite a bit here
//nolint:somelinter,anotherlinter,yetanotherlinter // long nolint directive that is over the limit
func Foo() {}
`
	out := mustWrap(t, src, 60)
	if out != src {
		t.Errorf("expected directive comments to be left unchanged\ngot:\n%s", out)
	}
}

// TestURLNotSplit verifies that a URL word is not broken across lines — it
// lands intact on its own continuation line if it would push a line over.
func TestURLNotSplit(t *testing.T) {
	src := `package p

// See https://example.com/some/very/long/path/that/pushes/the/line/over/the/limit for details.
func Foo() {}
`
	out := mustWrap(t, src, 60)
	// The URL must appear intact somewhere in the output.
	url := "https://example.com/some/very/long/path/that/pushes/the/line/over/the/limit"
	if !strings.Contains(out, url) {
		t.Errorf("URL was split or removed in output:\n%s", out)
	}
	// The URL should be on its own comment line (possibly with trailing text).
	foundURL := false
	for _, l := range strings.Split(out, "\n") {
		trimmed := strings.TrimPrefix(l, "// ")
		if strings.Contains(trimmed, url) {
			foundURL = true
		}
	}
	if !foundURL {
		t.Errorf("URL not found on a comment line in output:\n%s", out)
	}
}

// TestIndentedComment verifies that indented comments (inside functions) are
// wrapped correctly and the indent is preserved on continuation lines.
func TestIndentedComment(t *testing.T) {
	src := `package p

func Foo() {
	// The quick brown fox jumps over the lazy dog and then some more words here now.
}
`
	out := mustWrap(t, src, 60)
	for _, l := range strings.Split(out, "\n") {
		trimmed := strings.TrimLeft(l, "\t ")
		if strings.HasPrefix(trimmed, "// ") && len(l) > 60 {
			t.Errorf("indented comment line still over limit (%d chars): %q", len(l), l)
		}
	}
	// Continuation lines must carry the same indent.
	commentLines := []string{}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "// ") {
			commentLines = append(commentLines, l)
		}
	}
	if len(commentLines) < 2 {
		t.Errorf("expected indented comment to be split; got:\n%s", out)
	}
	for _, cl := range commentLines {
		if !strings.HasPrefix(cl, "\t") {
			t.Errorf("continuation comment line lost indent: %q", cl)
		}
	}
}

// TestMultipleCommentGroups verifies that multiple separate over-limit comment
// groups in the same file are all wrapped.
func TestMultipleCommentGroups(t *testing.T) {
	src := `package p

// This is the first long comment that definitely exceeds the sixty character limit set here.

// This is the second long comment that also definitely exceeds the sixty character limit too.
func Foo() {}
`
	out := mustWrap(t, src, 60)
	longLines := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "// ") && len(l) > 60 {
			longLines++
		}
	}
	if longLines > 0 {
		t.Errorf("%d comment lines still over limit after wrapping:\n%s", longLines, out)
	}
}

// TestAlreadyWrapped verifies that a comment that is already properly wrapped
// is returned unchanged.
func TestAlreadyWrapped(t *testing.T) {
	src := `package p

// Short line one.
// Short line two.
func Foo() {}
`
	out := mustWrap(t, src, 60)
	if out != src {
		t.Errorf("expected no change for already-wrapped comment\ngot:\n%s", out)
	}
}

// TestParagraphReflow verifies that two consecutive over-limit lines are
// treated as a single paragraph and reflowed together, producing a better
// result than splitting each line independently.
func TestParagraphReflow(t *testing.T) {
	// Each line is slightly over 60 chars.  Independent wrapping would produce
	// 4 lines; paragraph reflow should produce 3 (or fewer).
	src := `package p

// The quick brown fox jumps over the lazy dog one two three
// four five six seven eight nine ten eleven twelve thirteen.
func Foo() {}
`
	out := mustWrap(t, src, 60)

	commentLines := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "// ") {
			commentLines++
			if len(l) > 60 {
				t.Errorf("line over limit (%d): %q", len(l), l)
			}
		}
	}
	if commentLines >= 4 {
		t.Errorf("paragraph reflow produced %d lines (expected < 4); paragraph was not joined:\n%s",
			commentLines, out)
	}
}

// TestParagraphBreakAtBlankComment verifies that a blank // line between two
// comment groups causes them to be treated as separate paragraphs, each
// reflowed independently, and that the blank // line is preserved verbatim.
func TestParagraphBreakAtBlankComment(t *testing.T) {
	src := `package p

// The quick brown fox jumps over the lazy dog alpha beta gamma delta epsilon.
//
// The quick brown fox jumps over the lazy dog zeta eta theta iota kappa.
func Foo() {}
`
	out := mustWrap(t, src, 60)

	// The blank comment line must still be present.
	if !strings.Contains(out, "\n//\n") {
		t.Errorf("blank comment line was removed or altered:\n%s", out)
	}
	// No comment line should exceed the limit.
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "// ") && len(l) > 60 {
			t.Errorf("line over limit (%d): %q", len(l), l)
		}
	}
}
