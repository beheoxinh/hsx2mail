package keyring

import "testing"

func TestNew(t *testing.T) {
	k := New()
	if k == nil {
		t.Fatal("New() returned nil, want non-nil Keyring")
	}
}
