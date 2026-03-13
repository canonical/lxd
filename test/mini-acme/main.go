package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// testECCP256 is an insecure, test-only key from RFC 9500, Section 2.3.
// It can be used in tests to avoid slow key generation.
var testECCP256 = func() *ecdsa.PrivateKey {
	block, _ := pem.Decode([]byte(strings.ReplaceAll(
		`-----BEGIN EC TESTING KEY-----
MHcCAQEEIObLW92AqkWunJXowVR2Z5/+yVPBaFHnEedDk5WJxk/BoAoGCCqGSM49
AwEHoUQDQgAEQiVI+I+3gv+17KN0RFLHKh5Vj71vc75eSOkyMsxFxbFsTNEMTLjV
uKFxOelIgsiZJXKZNCX0FBmrfpCkKklCcg==
-----END EC TESTING KEY-----`, "TESTING KEY", "PRIVATE KEY")))
	key, _ := x509.ParseECPrivateKey(block.Bytes)
	return key
}()

// mini-acme is a minimal ACME (RFC 8555) server for integration testing.
// It serves HTTPS (required by RFC 8555) and supports HTTP-01 challenge
// validation when a validation target address is provided.
func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: mini-acme <port> <ca-cert-path> [<validation-addr>]")
		os.Exit(1)
	}

	port := os.Args[1]
	caCertPath := os.Args[2]
	addr := "127.0.0.1:" + port

	var validationAddr string
	if len(os.Args) >= 4 {
		validationAddr = os.Args[3]
	}

	srv, err := newServer(addr, validationAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Write the CA certificate so that ACME clients can trust this server
	// (e.g. via LEGO_CA_CERTIFICATES).
	err = os.WriteFile(caCertPath, srv.caCertPEM, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing CA cert: %v\n", err)
		os.Exit(1)
	}

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, unix.SIGINT, unix.SIGTERM)

	go func() {
		<-sigchan
		_ = srv.httpServer.Close()
	}()

	fmt.Fprintf(os.Stderr, "mini-acme listening on %s (HTTPS)\n", addr)

	err = srv.listenAndServeTLS()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

type acmeServer struct {
	baseURL        string
	validationAddr string
	caKey          *ecdsa.PrivateKey
	caCert         *x509.Certificate
	caCertPEM      []byte
	tlsCert        tls.Certificate
	httpServer     *http.Server

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
	caKey := testECCP256

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mini-acme CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(6 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("Failed creating CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing CA certificate: %w", err)
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	// Generate a TLS serving certificate for 127.0.0.1 signed by the CA.
	tlsCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "mini-acme"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, caCert, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("Failed creating TLS certificate: %w", err)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{tlsCertDER},
		PrivateKey:  caKey,
	}

	s := &acmeServer{
		baseURL:        "https://" + addr,
		validationAddr: validationAddr,
		caKey:          caKey,
		caCert:         caCert,
		caCertPEM:      caCertPEM,
		tlsCert:        tlsCert,
		orders:         make(map[string]*order),
		challenges:     make(map[string]*challenge),
		certs:          make(map[string][]byte),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /directory", s.handleDirectory)
	mux.HandleFunc("HEAD /new-nonce", s.handleNewNonce)
	mux.HandleFunc("GET /new-nonce", s.handleNewNonce)
	mux.HandleFunc("POST /new-acct", s.handleNewAccount)
	mux.HandleFunc("POST /new-order", s.handleNewOrder)
	mux.HandleFunc("POST /authz/{id}", s.handleAuthz)
	mux.HandleFunc("POST /challenge/{id}", s.handleChallenge)
	mux.HandleFunc("POST /order/{id}/finalize", s.handleFinalize)
	mux.HandleFunc("POST /order/{id}", s.handleOrder)
	mux.HandleFunc("POST /cert/{id}", s.handleCert)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	return s, nil
}

// listenAndServeTLS starts the HTTPS server using the in-memory TLS certificate.
func (s *acmeServer) listenAndServeTLS() error {
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{s.tlsCert},
		MinVersion:   tls.VersionTLS13,
	}

	ln, err := tls.Listen("tcp", s.httpServer.Addr, tlsConfig)
	if err != nil {
		return err
	}

	return s.httpServer.Serve(ln)
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

	s.mu.Unlock()

	// Perform HTTP-01 validation if a validation address is configured.
	if s.validationAddr != "" && chal.status == "pending" && ord != nil {
		valid := s.validateHTTP01(chal.token)

		s.mu.Lock()
		if valid {
			chal.status = "valid"
		} else {
			chal.status = "invalid"
		}

		s.mu.Unlock()
	}

	// Auto-approve when no validation address is configured.
	if s.validationAddr == "" {
		chal.status = "valid"
	}

	s.addNonce(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "http-01",
		"url":    s.baseURL + "/challenge/" + id,
		"token":  chal.token,
		"status": chal.status,
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

	certDER, err := x509.CreateCertificate(rand.Reader, template, s.caCert, csr.PublicKey, s.caKey)
	if err != nil {
		return nil, err
	}

	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// Return the full chain: leaf + CA.
	return append(leafPEM, s.caCertPEM...), nil
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
