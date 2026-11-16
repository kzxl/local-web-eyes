package security

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

var (
	hiddenStyleRe = regexp.MustCompile(`(?i)(display\s*:\s*none|visibility\s*:\s*hidden|opacity\s*:\s*0(?:\.0+)?\s*(?:;|$)|font-size\s*:\s*0(?:px|em|rem|%)?\s*(?:;|$))`)
	multiSpaceRe  = regexp.MustCompile(`[ \t]{2,}`)
	multiNewline  = regexp.MustCompile(`\n{3,}`)
)

var dangerousTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"iframe":   true,
	"object":   true,
	"embed":    true,
	"template": true,
	"svg":      true,
}

// SanitizeHTML strips active scripts, HTML comments, hidden DOM elements, and
// invisible Unicode characters commonly abused for indirect prompt injection.
func SanitizeHTML(raw string) (string, error) {
	cleanedInput := CleanText(raw)
	doc, err := html.Parse(strings.NewReader(cleanedInput))
	if err != nil {
		return "", err
	}
	pruneNode(doc)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return "", err
	}
	return CleanText(buf.String()), nil
}

// SanitizeNode prunes active scripts, HTML comments, and hidden DOM elements in-place,
// and strips invisible/control characters from text nodes without re-serializing HTML.
func SanitizeNode(doc *html.Node) {
	if doc == nil {
		return
	}
	pruneNode(doc)
}

func cleanNodeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isInvisibleOrDangerousRune(r) || r == '\r' {
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CleanText removes zero-width, bidirectional override, and unsafe control characters.
func CleanText(s string) string {
	out := cleanNodeText(s)
	out = multiNewline.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// CleanInline collapses excessive horizontal spaces in short snippets and titles.
func CleanInline(s string) string {
	s = CleanText(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func pruneNode(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if shouldDropNode(c) {
			n.RemoveChild(c)
			c = next
			continue
		}
		if c.Type == html.TextNode {
			c.Data = cleanNodeText(c.Data)
		} else {
			pruneNode(c)
		}
		c = next
	}
}

func shouldDropNode(n *html.Node) bool {
	if n.Type == html.CommentNode {
		return true
	}
	if n.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(n.Data)
	if dangerousTags[tag] {
		return true
	}
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		val := strings.TrimSpace(attr.Val)
		switch key {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(val, "true") {
				return true
			}
		case "style":
			if hiddenStyleRe.MatchString(val) {
				return true
			}
		}
	}
	return false
}

func isInvisibleOrDangerousRune(r rune) bool {
	switch r {
	case '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff', '\u00ad':
		return true
	}
	// Bidirectional override / isolate controls.
	if (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
		return true
	}
	return false
}
