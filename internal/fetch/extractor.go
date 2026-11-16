package fetch

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	trafilatura "github.com/markusmobius/go-trafilatura/v2"
	"github.com/local-web-eyes/local-web-eyes/internal/security"
	"golang.org/x/net/html"
)

var (
	headingLineRe = regexp.MustCompile(`(?m)^#{1,4}\s+(.+)$`)
	blankLinesRe  = regexp.MustCompile(`\n{3,}`)
)

// ExtractedDoc holds cleaned markdown, heading outline, links, and metadata.
type ExtractedDoc struct {
	Title       string            `json:"title"`
	Markdown    string            `json:"markdown"`
	Headings    []string          `json:"headings"`
	Links       []string          `json:"-"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	NeedsRender bool              `json:"-"`
}

// ExtractHTML sanitizes raw HTML, strips boilerplate via go-trafilatura, and converts to Markdown.
func ExtractHTML(rawHTML, rawURL string, minChars int) (ExtractedDoc, error) {
	if minChars <= 0 {
		minChars = 200
	}
	sanitized, err := security.SanitizeHTML(rawHTML)
	if err != nil {
		return ExtractedDoc{}, err
	}

	parsedURL, _ := url.Parse(rawURL)
	docNode, err := html.Parse(strings.NewReader(sanitized))
	if err != nil {
		return ExtractedDoc{}, err
	}

	pageTitle := extractHTMLTitle(docNode)
	links := extractLinks(docNode, parsedURL)

	res, extErr := trafilatura.ExtractDocument(docNode, trafilatura.Options{
		OriginalURL:     parsedURL,
		EnableFallback:  true,
		ExcludeComments: true,
		IncludeLinks:    true,
		Deduplicate:     true,
	})

	var mdOut string
	meta := make(map[string]string)

	if extErr == nil && res != nil {
		if strings.TrimSpace(res.Metadata.Title) != "" {
			pageTitle = security.CleanInline(res.Metadata.Title)
		}
		if res.Metadata.Author != "" {
			meta["author"] = security.CleanInline(res.Metadata.Author)
		}
		if !res.Metadata.Date.IsZero() {
			meta["published_date"] = res.Metadata.Date.Format("2006-01-02")
		}
		if res.Metadata.Sitename != "" {
			meta["site_name"] = security.CleanInline(res.Metadata.Sitename)
		}

		if res.ContentNode != nil {
			var buf bytes.Buffer
			if renderErr := html.Render(&buf, res.ContentNode); renderErr == nil {
				if conv, convErr := htmltomarkdown.ConvertString(buf.String()); convErr == nil {
					mdOut = conv
				}
			}
		}
		if strings.TrimSpace(mdOut) == "" {
			mdOut = res.ContentText
		}
	}

	// Fallback: convert sanitized HTML directly if trafilatura produced nothing.
	if strings.TrimSpace(mdOut) == "" {
		if conv, convErr := htmltomarkdown.ConvertString(sanitized); convErr == nil {
			mdOut = conv
		}
	}

	mdOut = security.CleanText(mdOut)
	mdOut = blankLinesRe.ReplaceAllString(mdOut, "\n\n")
	mdOut = strings.TrimSpace(mdOut)

	headings := extractMarkdownHeadings(mdOut, 25)
	if len(headings) == 0 {
		headings = extractDOMHeadings(docNode, 25)
	}
	needsRender := len(mdOut) < minChars && LooksLikeSPAShell(rawHTML, mdOut)

	return ExtractedDoc{
		Title:       security.CleanInline(pageTitle),
		Markdown:    mdOut,
		Headings:    headings,
		Links:       links,
		Metadata:    meta,
		NeedsRender: needsRender,
	}, nil
}

// LooksLikeSPAShell detects common empty client-side app mounts or anti-bot interstitials.
func LooksLikeSPAShell(rawHTML, extracted string) bool {
	if len(strings.TrimSpace(extracted)) >= 300 {
		return false
	}
	lower := strings.ToLower(rawHTML)
	markers := []string{
		`id="root"`, `id='root'`,
		`id="app"`, `id='app'`,
		`id="__next"`, `__next_data__`,
		`enable javascript`, `please turn javascript on`,
		`checking your browser`, `just a moment...`,
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// PaginateMarkdown returns a UTF-8 safe slice starting at offset up to maxChars,
// snapping back to a line/paragraph boundary to keep markdown intact.
func PaginateMarkdown(text string, offset, maxChars int) (chunk string, nextOffset int, truncated bool) {
	total := len(text)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return "", 0, false
	}
	if maxChars <= 0 {
		return text[offset:], 0, false
	}

	// Align offset forward to valid UTF-8 rune boundary.
	for offset < total && !utf8.RuneStart(text[offset]) {
		offset++
	}

	end := offset + maxChars
	if end >= total {
		return text[offset:], 0, false
	}

	// Align end backward to valid UTF-8 rune boundary.
	for end > offset && !utf8.RuneStart(text[end]) {
		end--
	}

	// Prefer breaking on paragraph or newline boundary in the latter half of the window.
	window := text[offset:end]
	if idx := strings.LastIndex(window, "\n\n"); idx > len(window)/2 {
		end = offset + idx
	} else if idx := strings.LastIndexByte(window, '\n'); idx > len(window)/2 {
		end = offset + idx
	}

	chunk = strings.TrimRight(text[offset:end], " \t\n")
	nextOffset = end
	for nextOffset < total && (text[nextOffset] == '\n' || text[nextOffset] == ' ') {
		nextOffset++
	}
	if nextOffset >= total {
		nextOffset = 0
	}
	return chunk, nextOffset, nextOffset > 0
}

func extractMarkdownHeadings(md string, maxHeadings int) []string {
	matches := headingLineRe.FindAllStringSubmatch(md, maxHeadings)
	out := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		h := strings.TrimSpace(m[1])
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

func extractHTMLTitle(n *html.Node) string {
	var title string
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if title != "" {
			return
		}
		if cur.Type == html.ElementNode && strings.EqualFold(cur.Data, "title") && cur.FirstChild != nil {
			title = strings.TrimSpace(cur.FirstChild.Data)
			return
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return title
}

func extractDOMHeadings(n *html.Node, maxHeadings int) []string {
	var out []string
	seen := make(map[string]struct{})
	var walk func(*html.Node, bool)
	walk = func(cur *html.Node, skip bool) {
		if len(out) >= maxHeadings {
			return
		}
		if cur.Type == html.ElementNode {
			tag := strings.ToLower(cur.Data)
			if tag == "nav" || tag == "header" || tag == "footer" || tag == "aside" {
				skip = true
			}
			if !skip && (tag == "h1" || tag == "h2" || tag == "h3" || tag == "h4") {
				txt := security.CleanInline(nodeText(cur))
				if txt != "" {
					if _, ok := seen[txt]; !ok {
						seen[txt] = struct{}{}
						out = append(out, txt)
					}
				}
			}
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}
	}
	walk(n, false)
	return out
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func extractLinks(n *html.Node, base *url.URL) []string {
	if base == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var links []string
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.ElementNode && strings.EqualFold(cur.Data, "a") {
			for _, a := range cur.Attr {
				if strings.EqualFold(a.Key, "href") {
					href := strings.TrimSpace(a.Val)
					if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
						break
					}
					ref, err := url.Parse(href)
					if err != nil {
						break
					}
					resolved := base.ResolveReference(ref)
					resolved.Fragment = ""
					s := resolved.String()
					if _, ok := seen[s]; !ok {
						seen[s] = struct{}{}
						links = append(links, s)
					}
					break
				}
			}
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return links
}
