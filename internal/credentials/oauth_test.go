package credentials

import (
	"testing"
	"time"
)

func TestIsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "expired token",
			expiresAt: time.Now().Add(-1 * time.Hour),
			want:      true,
		},
		{
			name:      "valid token",
			expiresAt: time.Now().Add(1 * time.Hour),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := &OAuthTokens{ExpiresAt: tt.expiresAt}
			got := tokens.IsExpired()
			if got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsExpiringSoon_True(t *testing.T) {
	tokens := &OAuthTokens{
		ExpiresAt: time.Now().Add(2 * time.Minute),
	}

	if !tokens.IsExpiringSoon(5 * time.Minute) {
		t.Error("IsExpiringSoon(5m) = false, want true for token expiring in 2 minutes")
	}
}

func TestIsExpiringSoon_False(t *testing.T) {
	tokens := &OAuthTokens{
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}

	if tokens.IsExpiringSoon(5 * time.Minute) {
		t.Error("IsExpiringSoon(5m) = true, want false for token expiring in 10 minutes")
	}
}
