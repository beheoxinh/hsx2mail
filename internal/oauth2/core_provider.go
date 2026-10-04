package oauth2

// coreProvider is Email Hub core's CredentialsProvider. It owns every slot:
//
//   - `google-mail` — mail's verified Google client (GoogleClientID).
//   - `google-contacts` / `google-calendar` — both resolve to the shared
//     `GoogleTestingClientID` (the un-Google-verified test project). One
//     client backs every extension that needs broader Google scopes.
//   - `microsoft-mail` / `microsoft-contacts` / `microsoft-calendar` — all
//     three resolve to `MicrosoftClientID`. Microsoft Graph doesn't gate
//     scopes behind verification the way Google does, so a single Azure AD
//     app registration covers Mail + Contacts + Calendar.
//
// No per-extension OAuth credentials live in the extension packages — the
// vars + ldflags + .env all consolidate here. Extensions stay focused on
// domain logic.
//
// Registered automatically at package init.
type coreProvider struct{}

// Shipped ids let a fresh install authorize mail without any .env, shim, or
// ldflags. Explicit configuration always takes precedence over these (see
// applyShippedFallbacks). Client ids are public identifiers by definition —
// they name the app to the provider the same way a username does — so embedding
// them here is not a secret leak. The client *secret* is never embedded: it
// arrives only via ldflags / shim / .env.
const (
	// ShippedGoogleClientID is the app's own Google OAuth client. It is a
	// Web-application client, so the refresh flow needs a secret, and that
	// secret comes only from configuration, never from this file. The shipped
	// id on its own still buys a correct authorize URL and the "not
	// configured" dialog stays quiet only when an id is present; the token
	// exchange then succeeds exactly when a matching secret is configured.
	//
	// RISK NOTE: if Google ever blocks this client under the mail.google.com
	// restricted scope (the same class of block Google shows as "This app
	// tried to access sensitive info ... Google blocked this access"), rotate
	// to a new dedicated client id + secret and set this back to "".
	ShippedGoogleClientID = "923491339165-r3saqrukpb74ub95effp2h44gfjjvptf.apps.googleusercontent.com"

	// ShippedMicrosoftClientID is Thunderbird's Microsoft OAuth client id.
	// It is a public client: PKCE S256 stands in for the client secret, and
	// Microsoft confirms the id is registered by answering a bad refresh with
	// invalid_grant (AADSTS9002313) rather than invalid_client (AADSTS7000215).
	ShippedMicrosoftClientID = "0816227c-ae3d-4470-9e1e-73c183e16b94"
)

func (coreProvider) Lookup(configID string) (ClientCredentials, bool) {
	switch configID {
	case "google-mail":
		if GoogleClientID == "" {
			return ClientCredentials{}, false
		}
		return ClientCredentials{ClientID: GoogleClientID, ClientSecret: GoogleClientSecret}, true
	case "google-contacts", "google-calendar":
		if GoogleTestingClientID == "" {
			return ClientCredentials{}, false
		}
		return ClientCredentials{ClientID: GoogleTestingClientID, ClientSecret: GoogleTestingClientSecret}, true
	case "microsoft-mail", "microsoft-contacts", "microsoft-calendar":
		if MicrosoftClientID == "" {
			return ClientCredentials{}, false
		}
		// Microsoft desktop apps omit the client secret (uses PKCE).
		return ClientCredentials{ClientID: MicrosoftClientID, ClientSecret: ""}, true
	default:
		return ClientCredentials{}, false
	}
}

// applyShippedFallbacks fills in the shipped client id for any provider the
// build was not given one for. It runs after ldflags, shim and .env have been
// read, so a genuinely configured value always wins over the shipped one.
//
// The fallback deliberately fills only ClientID, never ClientSecret. A wrong
// secret is a hard invalid_client failure at refresh time; a missing secret is
// handled by the PKCE exchange for public clients, and for confidential clients
// it surfaces honestly as "not configured" instead of failing silently later.
func applyShippedFallbacks() {
	if GoogleClientID == "" {
		GoogleClientID = ShippedGoogleClientID
	}
	if MicrosoftClientID == "" {
		MicrosoftClientID = ShippedMicrosoftClientID
	}
}

func init() {
	RegisterCredentialsProvider(coreProvider{})
}
