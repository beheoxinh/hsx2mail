package oauth2

import (
	"fmt"
	"regexp"
	"strings"
)

// MicrosoftTenant presets for the OAuth2 authorization endpoint.
//   - "common" lets Microsoft route personal + work/school accounts.
//   - "organizations" restricts to work/school accounts in any tenant.
//   - "consumers" restricts to personal Microsoft accounts.
//   - Any other non-empty value is treated as a tenant ID (GUID) or verified
//     domain, routed to that tenant's endpoint directly.
const (
	MicrosoftTenantCommon        = "common"
	MicrosoftTenantOrganizations = "organizations"
	MicrosoftTenantConsumers     = "consumers"
)

var microsoftTenantPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// NormalizeMicrosoftTenant cleans a user-supplied tenant value.
// Empty, "common", "organizations", "consumers" are returned as-is (lowercased
// for the presets). Anything else must look like a tenant ID or domain name,
// otherwise an error is returned.
func NormalizeMicrosoftTenant(tenant string) (string, error) {
	t := strings.TrimSpace(tenant)
	if t == "" {
		return MicrosoftTenantCommon, nil
	}
	lower := strings.ToLower(t)
	switch lower {
	case MicrosoftTenantCommon, MicrosoftTenantOrganizations, MicrosoftTenantConsumers:
		return lower, nil
	}
	if strings.Contains(t, "/") || strings.Contains(t, "?") || strings.Contains(t, "#") || strings.ContainsAny(t, " \t\n\r") {
		return "", fmt.Errorf("invalid Microsoft tenant %q", tenant)
	}
	if !microsoftTenantPattern.MatchString(t) {
		return "", fmt.Errorf("invalid Microsoft tenant %q", tenant)
	}
	return t, nil
}

// MicrosoftAuthorityEndpoints builds the authorize + token endpoints for a tenant.
func MicrosoftAuthorityEndpoints(tenant string) (authURL, tokenURL string, err error) {
	t, err := NormalizeMicrosoftTenant(tenant)
	if err != nil {
		return "", "", err
	}
	base := "https://login.microsoftonline.com/" + t + "/oauth2/v2.0"
	return base + "/authorize", base + "/token", nil
}

// MicrosoftProviderWithTenant returns the mail OAuth2 configuration for
// Microsoft/Outlook routed to a specific tenant authority. Empty tenant falls
// back to "common", preserving the stock behaviour.
func MicrosoftProviderWithTenant(tenant string) (ProviderConfig, error) {
	authURL, tokenURL, err := MicrosoftAuthorityEndpoints(tenant)
	if err != nil {
		return ProviderConfig{}, err
	}
	cfg := MicrosoftProvider()
	cfg.AuthURL = authURL
	cfg.TokenURL = tokenURL
	return overlayResolvedCreds(cfg, "microsoft-mail"), nil
}
