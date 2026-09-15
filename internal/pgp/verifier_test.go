package pgp

import "testing"

func TestIsPGPSigned(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{
			name:        "pgp-signature protocol",
			contentType: `multipart/signed; protocol="application/pgp-signature"; boundary=foo`,
			want:        true,
		},
		{
			name:        "text/plain",
			contentType: "text/plain",
			want:        false,
		},
		{
			name:        "empty string",
			contentType: "",
			want:        false,
		},
		{
			name:        "pkcs7-signature protocol",
			contentType: `multipart/signed; protocol="application/pkcs7-signature"; boundary=foo`,
			want:        false,
		},
		{
			name:        "multipart/signed without protocol",
			contentType: `multipart/signed; boundary=foo`,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPGPSigned(tt.contentType)
			if got != tt.want {
				t.Errorf("IsPGPSigned(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestIsPGPEncrypted(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{
			name:        "pgp-encrypted protocol",
			contentType: `multipart/encrypted; protocol="application/pgp-encrypted"; boundary=foo`,
			want:        true,
		},
		{
			name:        "text/plain",
			contentType: "text/plain",
			want:        false,
		},
		{
			name:        "empty string",
			contentType: "",
			want:        false,
		},
		{
			name:        "multipart/encrypted without protocol",
			contentType: `multipart/encrypted; boundary=foo`,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPGPEncrypted(tt.contentType)
			if got != tt.want {
				t.Errorf("IsPGPEncrypted(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}
