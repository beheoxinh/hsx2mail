package email

import (
	"strings"
	"testing"
)

// TestStripUnsafeNavigationSchemes covers 5-08: bluemonday applies one scheme
// list to every URL attribute, so `data:text/html` must be removed explicitly
// from navigable attributes.
func TestStripUnsafeNavigationSchemes(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		mustHave   []string
		mustNotHav []string
	}{
		{
			name:       "data html on href",
			in:         `<a href="data:text/html,<script>alert(1)</script>">x</a>`,
			mustNotHav: []string{"data:text/html"},
			mustHave:   []string{`<a href="">`},
		},
		{
			name:       "javascript on href",
			in:         `<a href="javascript:alert(1)">x</a>`,
			mustNotHav: []string{"javascript:"},
		},
		{
			name:       "vbscript on href",
			in:         `<a href="VBScript:msgbox(1)">x</a>`,
			mustNotHav: []string{"VBScript:", "vbscript:"},
		},
		{
			name:       "file scheme",
			in:         `<a href="file:///etc/passwd">x</a>`,
			mustNotHav: []string{"file:"},
		},
		{
			name:     "http kept",
			in:       `<a href="https://example.test/a?b=1#c">x</a>`,
			mustHave: []string{`href="https://example.test/a?b=1#c"`},
		},
		{
			name:     "mailto kept",
			in:       `<a href="mailto:a@b.test">x</a>`,
			mustHave: []string{`href="mailto:a@b.test"`},
		},
		{
			name:     "cid kept",
			in:       `<img src="cid:logo@example.test">`,
			mustHave: []string{`src="cid:logo@example.test"`},
		},
		{
			name:     "data image src kept",
			in:       `<img src="data:image/png;base64,iVBORw0KGgo=">`,
			mustHave: []string{`src="data:image/png;base64,iVBORw0KGgo="`},
		},
		{
			name:       "formaction",
			in:         `<button formaction="javascript:alert(1)">x</button>`,
			mustNotHav: []string{"javascript:"},
		},
		{
			name:       "cite",
			in:         `<blockquote cite="data:text/html,x">q</blockquote>`,
			mustNotHav: []string{"data:text/html"},
		},
		{
			name:     "anchor name kept",
			in:       `<a href="#section">x</a>`,
			mustHave: []string{`href="#section"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripUnsafeNavigationSchemes(tc.in)
			for _, bad := range tc.mustNotHav {
				if strings.Contains(got, bad) {
					t.Errorf("output still contains %q:\n%s", bad, got)
				}
			}
			for _, good := range tc.mustHave {
				if !strings.Contains(got, good) {
					t.Errorf("output lost %q:\n%s", good, got)
				}
			}
		})
	}
}

// TestSanitizeDropsDataNavigation is the same check through the real policy.
func TestSanitizeDropsDataNavigation(t *testing.T) {
	s := NewSanitizer()
	got := s.Sanitize(`<p>hi</p><a href="data:text/html;base64,PHNjcmlwdD4=">click</a><img src="data:image/png;base64,iVBORw0KGgo=">`)

	if strings.Contains(got, "data:text/html") {
		t.Errorf("data: navigation survived Sanitize:\n%s", got)
	}
	if !strings.Contains(got, "data:image/png") {
		t.Errorf("inline data: image was destroyed:\n%s", got)
	}
	if !strings.Contains(got, "<p>hi</p>") {
		t.Errorf("body text was destroyed:\n%s", got)
	}
}

// TestComposerSanitizerIsScriptFree is the send-path check: HTML the user
// authored must not ship active content to recipients.
func TestComposerSanitizerIsScriptFree(t *testing.T) {
	c := NewComposerSanitizer()

	cases := []struct{ name, in string }{
		{"script tag", `<p>hi</p><script>fetch('https://evil.test')</script>`},
		{"event handler", `<p onclick="alert(1)">hi</p>`},
		{"img onerror", `<img src="x" onerror="alert(1)">`},
		{"iframe", `<iframe src="https://evil.test"></iframe>`},
		{"object", `<object data="https://evil.test/x.swf"></object>`},
		{"embed", `<embed src="https://evil.test/x.swf">`},
		{"svg onload", `<svg onload="alert(1)"></svg>`},
		{"form", `<form action="javascript:alert(1)"><input></form>`},
		{"style expression", `<div style="width:expression(alert(1))">x</div>`},
		{"meta refresh", `<meta http-equiv="refresh" content="0;url=https://evil.test">`},
		{"link stylesheet", `<link rel="stylesheet" href="https://evil.test/x.css">`},
		{"data html anchor", `<a href="data:text/html,<script>x</script>">x</a>`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Sanitize(tc.in)
			lower := strings.ToLower(got)
			for _, bad := range []string{"<script", "onerror", "onclick", "onload", "<iframe", "<object", "<embed", "javascript:", "data:text/html", "<form", "expression("} {
				if strings.Contains(lower, bad) {
					t.Errorf("active content %q survived:\n%s", bad, got)
				}
			}
		})
	}
}

// TestComposerSanitizerKeepsRichText guards against over-sanitizing the editor's
// own output.
func TestComposerSanitizerKeepsRichText(t *testing.T) {
	c := NewComposerSanitizer()
	got := c.Sanitize(`<p>Hello <strong>world</strong> and <em>others</em></p>` +
		`<ul><li>one</li><li>two</li></ul>` +
		`<table><tr><td>cell</td></tr></table>` +
		`<blockquote>quoted</blockquote><pre><code>x := 1</code></pre>` +
		`<a href="https://example.test">link</a><a href="mailto:a@b.test">mail</a>`)

	for _, want := range []string{"<strong>", "<em>", "<ul>", "<li>", "<table>", "<td>", "<blockquote>", "<pre>", "<code>", `href="https://example.test"`, `href="mailto:a@b.test"`} {
		if !strings.Contains(got, want) {
			t.Errorf("composer sanitizer dropped %q:\n%s", want, got)
		}
	}
}
