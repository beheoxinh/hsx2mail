package oauth2

import (
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
)

// TestCallbackErrorPageEscapes covers 5-03: `error` and `error_description` come
// from the query string, so they must never reach the HTML unescaped.
func TestCallbackErrorPageEscapes(t *testing.T) {
	tests := []struct {
		name        string
		errParam    string
		descParam   string
		mustNotHave []string
	}{
		{
			name:        "script tag in error",
			errParam:    `<script>alert(1)</script>`,
			descParam:   "x",
			mustNotHave: []string{"<script>", "</script>"},
		},
		{
			name:        "img onerror in description",
			errParam:    "access_denied",
			descParam:   `<img src=x onerror=alert(1)>`,
			mustNotHave: []string{"<img"},
		},
		{
			name:        "attribute break-out",
			errParam:    `" autofocus onfocus="alert(1)`,
			descParam:   `</span><svg/onload=alert(1)>`,
			mustNotHave: []string{"</span><svg/onload=", `onfocus="`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCallbackServer()

			target := "/callback?error=" + urlEncode(tc.errParam) +
				"&error_description=" + urlEncode(tc.descParam)
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()

			s.handleCallback(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			body := rec.Body.String()
			for _, bad := range tc.mustNotHave {
				if strings.Contains(body, bad) {
					t.Fatalf("unescaped %q in response body:\n%s", bad, body)
				}
			}
			// The escaped text must still be present.
			if !strings.Contains(body, "&lt;") && strings.ContainsAny(tc.errParam+tc.descParam, "<>\"") {
				t.Fatalf("expected HTML-escaped output, got:\n%s", body)
			}
			if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
				t.Fatalf("missing restrictive CSP, got %q", csp)
			}
		})
	}
}

// TestCallbackSuccessPageHasCSP makes sure the happy path is also covered.
func TestCallbackSuccessPageHasCSP(t *testing.T) {
	s := NewCallbackServer()
	req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=xyz", nil)
	rec := httptest.NewRecorder()

	s.handleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("missing restrictive CSP, got %q", csp)
	}
}

func urlEncode(s string) string {
	return neturl.QueryEscape(s)
}
