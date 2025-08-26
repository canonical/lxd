package storage

import (
	"log/slog"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
	"golang.org/x/text/language"
)

const (
	// CustomScope is an example for how to use custom scopes in this library
	// (in this scenario, when requested, it will return a custom claim).
	CustomScope = "custom_scope"

	// CustomClaim is an example for how to return custom claims with this library.
	CustomClaim = "custom_claim"

	// CustomScopeImpersonatePrefix is an example scope prefix for passing user id to impersonate using token exchage.
	CustomScopeImpersonatePrefix = "custom_scope:impersonate:"
)

// AuthRequest is a wrapper for the oidc.AuthRequest to implement the op.AuthRequest interface.
type AuthRequest struct {
	ID            string
	CreationDate  time.Time
	ApplicationID string
	CallbackURI   string
	TransferState string
	Prompt        []string
	UILocales     []language.Tag
	LoginHint     string
	MaxAuthAge    *time.Duration
	UserID        string
	Scopes        []string
	ResponseType  oidc.ResponseType
	Nonce         string
	CodeChallenge *OIDCCodeChallenge

	done     bool
	authTime time.Time
}

// LogValue allows you to define which fields will be logged.
// Implements the [slog.LogValuer].
func (a *AuthRequest) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", a.ID),
		slog.Time("creation_date", a.CreationDate),
		slog.Any("scopes", a.Scopes),
		slog.String("response_type", string(a.ResponseType)),
		slog.String("app_id", a.ApplicationID),
		slog.String("callback_uri", a.CallbackURI),
	)
}

// GetID returns the ID of the auth request.
func (a *AuthRequest) GetID() string {
	return a.ID
}

// GetACR returns the ACR of the auth request.
func (a *AuthRequest) GetACR() string {
	return "" // we won't handle acr in this example
}

// GetAMR returns the AMR of the auth request.
func (a *AuthRequest) GetAMR() []string {
	// this example only uses password for authentication
	if a.done {
		return []string{"pwd"}
	}
	return nil
}

// GetAudience returns the audience of the auth request.
func (a *AuthRequest) GetAudience() []string {
	return []string{a.ApplicationID} // this example will always just use the client_id as audience
}

// GetAuthTime returns the auth time of the auth request.
func (a *AuthRequest) GetAuthTime() time.Time {
	return a.authTime
}

// GetClientID returns the client ID of the auth request.
func (a *AuthRequest) GetClientID() string {
	return a.ApplicationID
}

// GetCodeChallenge returns the code challenge of the auth request.
func (a *AuthRequest) GetCodeChallenge() *oidc.CodeChallenge {
	return CodeChallengeToOIDC(a.CodeChallenge)
}

// GetNonce returns the nonce of the auth request.
func (a *AuthRequest) GetNonce() string {
	return a.Nonce
}

// GetRedirectURI returns the redirect URI of the auth request.
func (a *AuthRequest) GetRedirectURI() string {
	return a.CallbackURI
}

// GetResponseType returns the response type of the auth request.
func (a *AuthRequest) GetResponseType() oidc.ResponseType {
	return a.ResponseType
}

// GetResponseMode returns the response mode of the auth request.
func (a *AuthRequest) GetResponseMode() oidc.ResponseMode {
	return "" // we won't handle response mode in this example
}

// GetScopes returns the scopes of the auth request.
func (a *AuthRequest) GetScopes() []string {
	return a.Scopes
}

// GetState returns the transfer state of the auth request.
func (a *AuthRequest) GetState() string {
	return a.TransferState
}

// GetSubject returns the user id of the auth request.
func (a *AuthRequest) GetSubject() string {
	return a.UserID
}

// Done marks the AuthRequest as completed.
func (a *AuthRequest) Done() bool {
	return a.done
}

// PromptToInternal converts the oidc.Prompt to the internal representation.
func PromptToInternal(oidcPrompt oidc.SpaceDelimitedArray) []string {
	prompts := make([]string, 0, len(oidcPrompt))
	for _, oidcPrompt := range oidcPrompt {
		switch oidcPrompt {
		case oidc.PromptNone,
			oidc.PromptLogin,
			oidc.PromptConsent,
			oidc.PromptSelectAccount:
			prompts = append(prompts, oidcPrompt)
		}
	}
	return prompts
}

// MaxAgeToInternal returns the maximum age in the internal representation.
func MaxAgeToInternal(maxAge *uint) *time.Duration {
	if maxAge == nil {
		return nil
	}
	dur := time.Duration(*maxAge) * time.Second
	return &dur
}

func authRequestToInternal(authReq *oidc.AuthRequest, userID string) *AuthRequest {
	return &AuthRequest{
		CreationDate:  time.Now(),
		ApplicationID: authReq.ClientID,
		CallbackURI:   authReq.RedirectURI,
		TransferState: authReq.State,
		Prompt:        PromptToInternal(authReq.Prompt),
		UILocales:     authReq.UILocales,
		LoginHint:     authReq.LoginHint,
		MaxAuthAge:    MaxAgeToInternal(authReq.MaxAge),
		UserID:        userID,
		Scopes:        authReq.Scopes,
		ResponseType:  authReq.ResponseType,
		Nonce:         authReq.Nonce,
		CodeChallenge: &OIDCCodeChallenge{
			Challenge: authReq.CodeChallenge,
			Method:    string(authReq.CodeChallengeMethod),
		},
	}
}

// OIDCCodeChallenge represents the storage version of the oidc.CodeChallenge.
type OIDCCodeChallenge struct {
	Challenge string
	Method    string
}

// CodeChallengeToOIDC converts the storage CodeChallenge to the oidc.CodeChallenge.
func CodeChallengeToOIDC(challenge *OIDCCodeChallenge) *oidc.CodeChallenge {
	if challenge == nil {
		return nil
	}
	challengeMethod := oidc.CodeChallengeMethodPlain
	if challenge.Method == "S256" {
		challengeMethod = oidc.CodeChallengeMethodS256
	}
	return &oidc.CodeChallenge{
		Challenge: challenge.Challenge,
		Method:    challengeMethod,
	}
}

// RefreshTokenRequestFromBusiness will simply wrap the storage RefreshToken to implement the op.RefreshTokenRequest interface.
func RefreshTokenRequestFromBusiness(token *refreshToken) op.RefreshTokenRequest {
	return &RefreshTokenRequest{token}
}

// RefreshTokenRequest is a wrapper for the storage RefreshToken to implement the op.RefreshTokenRequest interface.
type RefreshTokenRequest struct {
	*refreshToken
}

// GetAMR returns the AMR of the token.
func (r *RefreshTokenRequest) GetAMR() []string {
	return r.AMR
}

// GetAudience returns the audience that the token belongs to.
func (r *RefreshTokenRequest) GetAudience() []string {
	return r.Audience
}

// GetAuthTime returns the auth time that the token was issued.
func (r *RefreshTokenRequest) GetAuthTime() time.Time {
	return r.AuthTime
}

// GetClientID returns the client id of the client that the token belongs to.
func (r *RefreshTokenRequest) GetClientID() string {
	return r.ApplicationID
}

// GetScopes returns the scope that the token belongs to.
func (r *RefreshTokenRequest) GetScopes() []string {
	return r.Scopes
}

// GetSubject returns the user id of the user that the token belongs to.
func (r *RefreshTokenRequest) GetSubject() string {
	return r.UserID
}

// SetCurrentScopes sets the current scopes of the token.
func (r *RefreshTokenRequest) SetCurrentScopes(scopes []string) {
	r.Scopes = scopes
}
