package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery/form"
	"github.com/pborman/uuid"
)

const formURL string = "/form"

type loginResponse struct {
	Token *httpbakery.DischargeToken `json:"token"`
}

// authService is an HTTP service for authentication using macaroons.
type authService struct {
	httpService

	KeyPair *bakery.KeyPair
	Checker credentialsChecker

	userTokens map[string]string // map user token to username
}

// newAuthService returns an AuthService.
func newAuthService(listenAddr string, logger *log.Logger) *authService {
	key := bakery.MustGenerateKey()
	mux := http.NewServeMux()
	s := authService{
		httpService: httpService{
			Name:       "auth",
			ListenAddr: listenAddr,
			Logger:     logger,
			Mux:        mux,
		},
		KeyPair:    key,
		Checker:    newCredentialsChecker(),
		userTokens: map[string]string{},
	}

	mux.Handle(formURL, http.HandlerFunc(s.formHandler))

	discharger := httpbakery.NewDischarger(
		httpbakery.DischargerParams{
			Key:     key,
			Checker: httpbakery.ThirdPartyCaveatCheckerFunc(s.thirdPartyChecker),
		})
	discharger.AddMuxHandlers(mux, "/")
	return &s
}

// thirdPartyChecker validates a third-party caveat and returns the username if valid.
func (s *authService) thirdPartyChecker(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
	if token == nil {
		err := httpbakery.NewInteractionRequiredError(nil, req)
		err.SetInteraction("form", form.InteractionInfo{URL: formURL})
		return nil, err
	}

	username, ok := s.userTokens[string(token.Value)]
	if token.Kind != "form" || !ok {
		return nil, fmt.Errorf("invalid token %#v", token)
	}

	_, _, err := checkers.ParseCaveat(string(info.Condition))
	if err != nil {
		return nil, fmt.Errorf("cannot parse caveat %q: %w", info.Condition, err)
	}

	return []checkers.Caveat{
		checkers.DeclaredCaveat("username", username),
	}, nil
}

// writeJSON writes 'val' as JSON to the HTTP response with the given status code.
func writeJSON(w http.ResponseWriter, code int, val any) error {
	data, err := json.Marshal(val)
	if err != nil {
		return err
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_, err = w.Write(data)
	return err
}

// formHandler handles the HTTP GET and POST requests for login form and validation.
func (s *authService) formHandler(w http.ResponseWriter, req *http.Request) {
	s.LogRequest(req)
	switch req.Method {
	case "GET":
		_ = writeJSON(w, http.StatusOK, schemaResponse)
	case "POST":
		content, err := io.ReadAll(req.Body)
		if err != nil {
			s.bakeryFail(w, "failed to read request: %v", err)
			return
		}

		loginRequest := form.LoginRequest{}
		loginRequest.Body = form.LoginBody{}
		err = json.Unmarshal(content, &loginRequest.Body)
		if err != nil {
			s.bakeryFail(w, "failed to parse credentials: %v", err)
			return
		}

		form := loginRequest.Body.Form

		if !s.Checker.Check(form) {
			s.bakeryFail(w, "invalid credentials")
			return
		}

		username := form["username"].(string)
		token := s.getRandomToken()
		s.userTokens[token] = username

		loginResponse := loginResponse{
			Token: &httpbakery.DischargeToken{
				Kind:  "form",
				Value: []byte(token),
			},
		}

		_ = writeJSON(w, http.StatusOK, loginResponse)

	default:
		s.Fail(w, http.StatusMethodNotAllowed, "%s method not allowed", req.Method)
		return
	}
}

// getRandomToken generates a random token using UUID and Base64 encoding.
func (s *authService) getRandomToken() string {
	uuid := []byte(uuid.New()[0:24])
	return base64.StdEncoding.EncodeToString(uuid)
}

// bakeryFail writes an HTTP bakery error response with the given message and arguments.
func (s *authService) bakeryFail(w http.ResponseWriter, msg string, args ...any) {
	httpbakery.WriteError(context.TODO(), w, fmt.Errorf(msg, args...))
}
