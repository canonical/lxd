package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/test/testutils/servemock"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// mini-acme is a minimal ACME (RFC 8555) server for integration testing.
// It serves HTTPS (required by RFC 8555) and supports HTTP-01 challenge
// validation when a validation target address is provided.
func run() error {
	if len(os.Args) < 3 {
		return errors.New("Usage: mini-acme <listen-addr> <ca-cert-path> [<validation-addr>]")
	}

	addr := os.Args[1]
	caCertPath := os.Args[2]

	var validationAddr string
	if len(os.Args) >= 4 {
		validationAddr = os.Args[3]
	}

	srv, err := newServer(addr, validationAddr)
	if err != nil {
		return err
	}

	result, err := servemock.API(context.Background(), servemock.Config{
		Address:  addr,
		NotFound: nil,
		Handlers: []servemock.Handler{
			{
				Pattern:     "GET /directory",
				HTTPHandler: srv.handleDirectory,
			},
			{
				Pattern:     "HEAD /new-nonce",
				HTTPHandler: srv.handleNewNonce,
			},
			{
				Pattern:     "GET /new-nonce",
				HTTPHandler: srv.handleNewNonce,
			},
			{
				Pattern:     "POST /new-acct",
				HTTPHandler: srv.handleNewAccount,
			},
			{
				Pattern:     "POST /new-order",
				HTTPHandler: srv.handleNewOrder,
			},
			{
				Pattern:     "POST /authz/{id}",
				HTTPHandler: srv.handleAuthz,
			},
			{
				Pattern:     "POST /challenge/{id}",
				HTTPHandler: srv.handleChallenge,
			},
			{
				Pattern:     "POST /order/{id}/finalize",
				HTTPHandler: srv.handleFinalize,
			},
			{
				Pattern:     "POST /order/{id}",
				HTTPHandler: srv.handleOrder,
			},
			{
				Pattern:     "POST /cert/{id}",
				HTTPHandler: srv.handleCert,
			},
		},
		CACertPath: caCertPath,
		UseTLS:     true,
	})
	if err != nil {
		return err
	}

	srv.certInfo = result.CertInfo
	srv.tlsConfig = result.TLSConfig
	return <-result.Err
}

type acmeServer struct {
	baseURL        string
	validationAddr string
	certInfo       *shared.CertInfo
	tlsConfig      *tls.Config

	mu         sync.Mutex
	orders     map[string]*order
	challenges map[string]*challenge
	certs      map[string][]byte
	nextID     int
}

type order struct {
	domain string
	status string
	certID string
}

type challenge struct {
	orderID string
	token   string
	status  string
}

func newServer(addr string, validationAddr string) (*acmeServer, error) {
	s := &acmeServer{
		baseURL:        "https://" + addr,
		validationAddr: validationAddr,
		orders:         make(map[string]*order),
		challenges:     make(map[string]*challenge),
		certs:          make(map[string][]byte),
	}

	return s, nil
}

func (s *acmeServer) allocID() string {
	s.nextID++
	return strconv.Itoa(s.nextID)
}

func (s *acmeServer) addNonce(w http.ResponseWriter) {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString(nonce))
	w.Header().Set("Cache-Control", "no-store")
}

func (s *acmeServer) handleDirectory(w http.ResponseWriter, _ *http.Request) {
	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"newNonce":   s.baseURL + "/new-nonce",
		"newAccount": s.baseURL + "/new-acct",
		"newOrder":   s.baseURL + "/new-order",
	})
}

func (s *acmeServer) handleNewNonce(w http.ResponseWriter, _ *http.Request) {
	s.addNonce(w)
	w.WriteHeader(http.StatusOK)
}

func (s *acmeServer) handleNewAccount(w http.ResponseWriter, _ *http.Request) {
	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", s.baseURL+"/acct/1")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "valid",
	})
}

func (s *acmeServer) handleNewOrder(w http.ResponseWriter, r *http.Request) {
	payload := parseJWSPayload(r)

	var req struct {
		Identifiers []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"identifiers"`
	}

	domain := "unknown"
	if json.Unmarshal(payload, &req) == nil && len(req.Identifiers) > 0 {
		domain = req.Identifiers[0].Value
	}

	s.mu.Lock()
	id := s.allocID()
	s.orders[id] = &order{
		domain: domain,
		status: "pending",
	}

	s.mu.Unlock()

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", s.baseURL+"/order/"+id)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "pending",
		"identifiers": []map[string]string{
			{"type": "dns", "value": domain},
		},
		"authorizations": []string{s.baseURL + "/authz/" + id},
		"finalize":       s.baseURL + "/order/" + id + "/finalize",
	})
}

func (s *acmeServer) handleAuthz(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	ord := s.orders[id]
	chal := s.challenges[id]
	s.mu.Unlock()

	if ord == nil {
		http.NotFound(w, r)
		return
	}

	chalStatus := "pending"
	authzStatus := "pending"
	if chal != nil && chal.status == "valid" {
		chalStatus = "valid"
		authzStatus = "valid"
	}

	// Auto-approve when no validation address is configured.
	if s.validationAddr == "" {
		chalStatus = "valid"
		authzStatus = "valid"
	}

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": authzStatus,
		"identifier": map[string]string{
			"type":  "dns",
			"value": ord.domain,
		},
		"challenges": []map[string]any{
			{
				"type":   "http-01",
				"url":    s.baseURL + "/challenge/" + id,
				"token":  "token-" + id,
				"status": chalStatus,
			},
		},
	})
}

func (s *acmeServer) handleChallenge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	ord := s.orders[id]
	chal := s.challenges[id]
	if chal == nil {
		chal = &challenge{
			orderID: id,
			token:   "token-" + id,
			status:  "pending",
		}

		s.challenges[id] = chal
	}

	token := chal.token
	chalStatus := chal.status

	s.mu.Unlock()

	// Perform HTTP-01 validation if a validation address is configured.
	if s.validationAddr != "" && chalStatus == "pending" && ord != nil {
		valid := s.validateHTTP01(token)

		s.mu.Lock()
		if valid {
			chal.status = "valid"
		} else {
			chal.status = "invalid"
		}

		token = chal.token
		chalStatus = chal.status
		s.mu.Unlock()
	}

	// Auto-approve when no validation address is configured.
	if s.validationAddr == "" {
		s.mu.Lock()
		chal.status = "valid"
		token = chal.token
		chalStatus = chal.status
		s.mu.Unlock()
	}

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "http-01",
		"url":    s.baseURL + "/challenge/" + id,
		"token":  token,
		"status": chalStatus,
	})
}

// validateHTTP01 performs HTTP-01 challenge validation by fetching the token
// from the validation target's /.well-known/acme-challenge/ endpoint.
func (s *acmeServer) validateHTTP01(token string) bool {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	url := "https://" + s.validationAddr + "/.well-known/acme-challenge/" + token
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP-01 validation failed for %s: %v\n", token, err)
		return false
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "HTTP-01 validation failed for %s: status %d\n", token, resp.StatusCode)
		return false
	}

	// The response must start with the token (full format is token.thumbprint).
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if !strings.HasPrefix(string(buf[:n]), token) {
		fmt.Fprintf(os.Stderr, "HTTP-01 validation failed for %s: unexpected response\n", token)
		return false
	}

	return true
}

func (s *acmeServer) handleOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	ord := s.orders[id]
	s.mu.Unlock()

	if ord == nil {
		http.NotFound(w, r)
		return
	}

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")

	resp := map[string]any{
		"status": ord.status,
		"identifiers": []map[string]string{
			{"type": "dns", "value": ord.domain},
		},
		"authorizations": []string{s.baseURL + "/authz/" + id},
		"finalize":       s.baseURL + "/order/" + id + "/finalize",
	}

	if ord.certID != "" {
		resp["certificate"] = s.baseURL + "/cert/" + ord.certID
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *acmeServer) handleFinalize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	ord := s.orders[id]
	s.mu.Unlock()

	if ord == nil {
		http.NotFound(w, r)
		return
	}

	payload := parseJWSPayload(r)

	var req struct {
		CSR string `json:"csr"`
	}

	err := json.Unmarshal(payload, &req)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	csrDER, err := base64.RawURLEncoding.DecodeString(req.CSR)
	if err != nil {
		http.Error(w, "bad CSR encoding", http.StatusBadRequest)
		return
	}

	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		http.Error(w, "bad CSR", http.StatusBadRequest)
		return
	}

	certPEM, err := s.issueCertificate(csr, ord.domain)
	if err != nil {
		http.Error(w, "failed issuing cert", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	certID := s.allocID()
	s.certs[certID] = certPEM
	ord.status = "valid"
	ord.certID = certID
	s.mu.Unlock()

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", s.baseURL+"/order/"+id)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "valid",
		"identifiers": []map[string]string{
			{"type": "dns", "value": ord.domain},
		},
		"authorizations": []string{s.baseURL + "/authz/" + id},
		"finalize":       s.baseURL + "/order/" + id + "/finalize",
		"certificate":    s.baseURL + "/cert/" + certID,
	})
}

func (s *acmeServer) handleCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	certPEM := s.certs[id]
	s.mu.Unlock()

	if certPEM == nil {
		http.NotFound(w, r)
		return
	}

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/pem-certificate-chain")
	_, _ = w.Write(certPEM)
}

func (s *acmeServer) issueCertificate(csr *x509.CertificateRequest, domain string) ([]byte, error) {
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      csr.Subject,
		DNSNames:     []string{domain},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, s.certInfo.CA(), csr.PublicKey, servemock.CAPrivateKey())
	if err != nil {
		return nil, err
	}

	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.certInfo.CA().Raw})

	// Return the full chain: leaf + CA.
	return append(leafPEM, caCertPEM...), nil
}

// parseJWSPayload extracts the decoded payload from a JWS request body.
// Returns nil for POST-as-GET requests (empty payload) or on error.
func parseJWSPayload(r *http.Request) []byte {
	var jws struct {
		Payload string `json:"payload"`
	}

	err := json.NewDecoder(r.Body).Decode(&jws)
	if err != nil {
		return nil
	}

	if jws.Payload == "" {
		return nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(jws.Payload)
	if err != nil {
		return nil
	}

	return payload
}
