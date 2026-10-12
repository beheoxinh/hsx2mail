package oauth2

import "testing"

func TestNormalizeMicrosoftTenant(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"", "common", false},
		{"common", "common", false},
		{"organizations", "organizations", false},
		{"consumers", "consumers", false},
		{"COMMON", "common", false},
		{"f01e930a-b52e-42b1-b70f-a8882b5d043b", "f01e930a-b52e-42b1-b70f-a8882b5d043b", false},
		{"contoso.onmicrosoft.com", "contoso.onmicrosoft.com", false},
		{"a/b", "", true},
		{"a b", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeMicrosoftTenant(c.in)
		if c.wantErr && err == nil {
			t.Errorf("input %q: want error, got none", c.in)
		}
		if !c.wantErr && (err != nil || got != c.want) {
			t.Errorf("input %q: got %q,%v want %q", c.in, got, err, c.want)
		}
	}
}

func TestMicrosoftAuthorityEndpoints(t *testing.T) {
	auth, token, err := MicrosoftAuthorityEndpoints("f01e930a-b52e-42b1-b70f-a8882b5d043b")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://login.microsoftonline.com/f01e930a-b52e-42b1-b70f-a8882b5d043b/oauth2/v2.0/authorize"
	if auth != want {
		t.Errorf("auth = %q want %q", auth, want)
	}
	if token == "" || len(token) < 10 {
		t.Errorf("bad token endpoint %q", token)
	}
	auth2, _, _ := MicrosoftAuthorityEndpoints("")
	if auth2 != "https://login.microsoftonline.com/common/oauth2/v2.0/authorize" {
		t.Errorf("default auth = %q", auth2)
	}
}
