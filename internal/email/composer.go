package email

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// cssExpressionRe matches the legacy-IE script-execution form inside a style
// attribute: `width:expression(alert(1))`. It is stripped in a post-pass
// because bluemonday's attribute builder only takes a regexp, and a regexp
// cannot express "any value that does not contain X".
var cssExpressionRe = regexp.MustCompile(`(?i)expression\s*\([^)]*\)`)

// ComposerSanitizer is a strict policy for HTML the user authored and is about
// to send. It is deliberately tighter than NewSanitizer: outgoing mail is not a
// third-party layout problem, so legacy presentational elements, remote media,
// and remote stylesheets are dropped rather than allowed.
//
// Its purpose is that the rich-text editor's output is never trusted to be
// script-free: pasted HTML and HTML pasted from a browser both carry active
// content, and it would be shipped to every recipient.
type ComposerSanitizer struct {
	policy *bluemonday.Policy
}

// NewComposerSanitizer returns a strict sanitizer for outgoing HTML bodies.
func NewComposerSanitizer() *ComposerSanitizer {
	p := bluemonday.NewPolicy()

	// Formatting only. bluemonday has no AllowStandardElements, so the set is
	// spelled out: no iframe, object, embed, script, style, link, meta, or form.
	p.AllowElements(
		"a", "abbr", "b", "bdi", "bdo", "big", "br", "caption", "cite", "code",
		"dd", "del", "dfn", "div", "dl", "dt", "em", "figcaption", "figure",
		"h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "ins", "kbd",
		"li", "mark", "ol", "p", "pre", "q", "s", "samp", "small", "span",
		"strong", "sub", "sup", "table", "tbody", "td", "tfoot", "th", "thead",
		"time", "tr", "tt", "u", "ul", "var", "wbr", "blockquote", "pre", "code",
	)
	p.AllowStandardAttributes()
	p.AllowStandardURLs()
	p.AllowLists()
	p.AllowTables()

	// Same restriction as display sanitizing: data: may not be a navigation
	// target. Inline images go through cid: attachments, not data:.
	p.AllowURLSchemes("http", "https", "mailto", "cid")
	p.AllowRelativeURLs(false)
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	p.AllowAttrs("href", "title", "name").OnElements("a", "area")
	p.AllowAttrs("class").Globally()
	// style is needed for the rich-text editor's formatting. `expression()` is
	// stripped: it is legacy-IE script execution inside a CSS value.
	p.AllowAttrs("style").Globally()

	return &ComposerSanitizer{policy: p}
}

// Sanitize strips everything active from an outgoing HTML body.
func (c *ComposerSanitizer) Sanitize(html string) string {
	return StripUnsafeNavigationSchemes(cssExpressionRe.ReplaceAllString(c.policy.Sanitize(html), ""))
}
