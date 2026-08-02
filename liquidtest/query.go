package liquidtest

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// walk visits n and its descendants depth-first until fn returns false.
func walk(n *html.Node, fn func(*html.Node) bool) bool {
	if !fn(n) {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if !walk(c, fn) {
			return false
		}
	}
	return true
}

// attr returns the named attribute of an element node.
func attr(n *html.Node, name string) (string, bool) {
	if n.Type != html.ElementNode {
		return "", false
	}
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

// matches reports whether an element satisfies a simple selector: "#id" by
// id, ".class" by class-list membership, anything else by tag name.
func matches(n *html.Node, selector string) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch {
	case strings.HasPrefix(selector, "#"):
		id, _ := attr(n, "id")
		return id == selector[1:]
	case strings.HasPrefix(selector, "."):
		classes, _ := attr(n, "class")
		for _, c := range strings.Fields(classes) {
			if c == selector[1:] {
				return true
			}
		}
		return false
	default:
		return n.Data == selector
	}
}

// textOf returns the trimmed text content of the first element in doc
// matching selector, failing the test when nothing matches.
func textOf(t testing.TB, doc *html.Node, selector string) string {
	t.Helper()
	var found *html.Node
	walk(doc, func(n *html.Node) bool {
		if matches(n, selector) {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		t.Fatalf("liquidtest: no element matches selector %q", selector)
	}
	var b strings.Builder
	walk(found, func(n *html.Node) bool {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		return true
	})
	return strings.TrimSpace(b.String())
}
