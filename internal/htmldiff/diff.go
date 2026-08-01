package htmldiff

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-monolith/ghtmx"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"
)

// DiffStrings compares expected and actual HTML after normalizing both sides:
// each is parsed with x/net/html and re-printed one node per line with
// insignificant whitespace collapsed, so formatting differences do not
// register as diffs. Returns the normalized actual value and the diff.
func DiffStrings(expected, actual string) (output, diff string, err error) {
	ne, err := Normalize(expected)
	if err != nil {
		return "", "", fmt.Errorf("expected html normalization error: %w", err)
	}
	na, err := Normalize(actual)
	if err != nil {
		return "", "", fmt.Errorf("actual html normalization error: %w", err)
	}
	return na, cmp.Diff(ne, na), nil
}

func Diff(input ghtmx.Component, expected string) (actual, diff string, err error) {
	return DiffCtx(context.Background(), input, expected)
}

func DiffCtx(ctx context.Context, input ghtmx.Component, expected string) (actual, diff string, err error) {
	var a strings.Builder
	err = input.Render(ctx, &a)
	if err != nil {
		return "", "", fmt.Errorf("failed to render input: %w", err)
	}
	// Golden update mode: rewrite expected.html in the test's working
	// directory with the raw render output instead of comparing. Guarded to
	// packages with exactly one expected*.html golden — a package with
	// several goldens (several Diff calls) would corrupt them and must be
	// updated by hand.
	if os.Getenv("GHTMX_UPDATE_GOLDEN") != "" {
		if matches, _ := filepath.Glob("expected*.html"); len(matches) == 1 && matches[0] == "expected.html" {
			if err := os.WriteFile("expected.html", []byte(a.String()), 0o644); err != nil {
				return "", "", fmt.Errorf("failed to update golden expected.html: %w", err)
			}
			return a.String(), "", nil
		}
	}
	return DiffStrings(expected, a.String())
}

// Normalize parses src as an HTML document and prints it in a canonical
// form: one element per line, indented, with runs of whitespace in text
// collapsed to a single space. Text inside pre and textarea is preserved
// verbatim, since whitespace is significant there.
func Normalize(src string) (string, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", err
	}
	sb := new(strings.Builder)
	printNode(sb, doc, 0, false)
	return sb.String(), nil
}

var voidElements = map[string]struct{}{
	"area": {}, "base": {}, "br": {}, "col": {}, "embed": {}, "hr": {},
	"img": {}, "input": {}, "link": {}, "meta": {}, "source": {},
	"track": {}, "wbr": {},
}

// whitespace-significant elements whose text content is printed verbatim.
var preformatted = map[string]struct{}{
	"pre": {}, "textarea": {},
}

func printNode(sb *strings.Builder, n *html.Node, depth int, verbatim bool) {
	switch n.Type {
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			printNode(sb, c, depth, verbatim)
		}
	case html.DoctypeNode:
		writeIndented(sb, depth, "<!doctype "+n.Data+">")
	case html.CommentNode:
		writeIndented(sb, depth, "<!--"+collapse(n.Data)+"-->")
	case html.TextNode:
		if verbatim {
			if n.Data != "" {
				sb.WriteString(n.Data)
				sb.WriteString("\n")
			}
			return
		}
		if text := collapse(n.Data); text != "" {
			writeIndented(sb, depth, text)
		}
	case html.ElementNode:
		open := new(strings.Builder)
		open.WriteString("<")
		open.WriteString(n.Data)
		for _, a := range n.Attr {
			open.WriteString(" ")
			if a.Namespace != "" {
				open.WriteString(a.Namespace)
				open.WriteString(":")
			}
			open.WriteString(a.Key)
			open.WriteString(`="`)
			open.WriteString(html.EscapeString(a.Val))
			open.WriteString(`"`)
		}
		open.WriteString(">")
		writeIndented(sb, depth, open.String())
		_, isVerbatim := preformatted[n.Data]
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			printNode(sb, c, depth+1, verbatim || isVerbatim)
		}
		if _, void := voidElements[n.Data]; !void {
			writeIndented(sb, depth, "</"+n.Data+">")
		}
	}
}

func writeIndented(sb *strings.Builder, depth int, s string) {
	for range depth {
		sb.WriteString("  ")
	}
	sb.WriteString(s)
	sb.WriteString("\n")
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
