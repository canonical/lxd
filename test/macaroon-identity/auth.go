package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"

	"golang.org/x/net/context"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/form"

	"github.com/juju/httprequest"
	"github.com/rogpeppe/fastuuid"
)

const formURL string = "/form"

type loginResponse struct {
	Token *httpbakery.DischargeToken `json:"token"`
}

// AuthService is an HTTP service for authentication using macaroons.
type authService struct {
	httpService

	KeyPair *bakery.KeyPair
	Checker credentialsChecker

	userTokens    map[string]string // map user token to username
	uuidGenerator *fastuuid.Generator
}

// NewAuthService returns an AuthService
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
		KeyPair:       key,
		Checker:       newCredentialsChecker(),
		uuidGenerator: fastuuid.MustNewGenerator(),
		userTokens:    map[string]string{},
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

func (s *authService) thirdPartyChecker(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
	if token == nil {
		err := httpbakery.NewInteractionRequiredError(nil, req)
		err.SetInteraction("form", form.InteractionInfo{URL: formURL})
		return nil, err
	}

	tokenString := string(token.Value)
	username, ok := s.userTokens[tokenString]
	if token.Kind != "form" || !ok {
		return nil, fmt.Errorf("invalid token %#v", token)
	}

	_, _, err := checkers.ParseCaveat(string(info.Condition))
	if err != nil {
		return nil, fmt.Errorf("cannot parse caveat %q: %s", info.Condition, err)
	}

	return []checkers.Caveat{
		checkers.DeclaredCaveat("username", username),
	}, nil
}

func (s *authService) formHandler(w http.ResponseWriter, req *http.Request) {
	s.LogRequest(req)
	switch req.Method {
	case "GET":
		httprequest.WriteJSON(w, http.StatusOK, schemaResponse)
	case "POST":
		params := httprequest.Params{
			Response: w,
			Request:  req,
			Context:  context.TODO(),
		}
		loginRequest := form.LoginRequest{}
		if err := httprequest.Unmarshal(params, &loginRequest); err != nil {
			s.bakeryFail(w, "can't unmarshal login request")
			return
		}

		form, err := fieldsChecker.Coerce(loginRequest.Body.Form, nil)
		if err != nil {
			s.bakeryFail(w, "invalid login form data: %v", err)
			return
		}

		if !s.Checker.Check(form) {
			s.bakeryFail(w, "invalid credentials")
			return
		}

		username := form.(map[string]interface{})["username"].(string)
		token := s.getRandomToken()
		s.userTokens[token] = username

		loginResponse := loginResponse{
			Token: &httpbakery.DischargeToken{
				Kind:  "form",
				Value: []byte(token),
			},
		}
		httprequest.WriteJSON(w, http.StatusOK, loginResponse)

	default:
		s.Fail(w, http.StatusMethodNotAllowed, "%s method not allowed", req.Method)
		return
	}
}

func (s *authService) getRandomToken() string {
	uuid := make([]byte, 24)
	for i, b := range s.uuidGenerator.Next() {
		uuid[i] = b
	}
	return base64.StdEncoding.EncodeToString(uuid)
}

func (s *authService) bakeryFail(w http.ResponseWriter, msg string, args ...interface{}) {
	httpbakery.WriteError(context.TODO(), w, fmt.Errorf(msg, args...))
}
