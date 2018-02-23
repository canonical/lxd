package util

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/context"

	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// WriteJSON encodes the body as JSON and sends it back to the client
func WriteJSON(w http.ResponseWriter, body interface{}, debug bool) error {
	var output io.Writer
	var captured *bytes.Buffer

	output = w
	if debug {
		captured = &bytes.Buffer{}
		output = io.MultiWriter(w, captured)
	}

	err := json.NewEncoder(output).Encode(body)

	if captured != nil {
		shared.DebugJson(captured)
	}

	return err
}

// EtagHash hashes the provided data and returns the sha256
func EtagHash(data interface{}) (string, error) {
	etag := sha256.New()
	err := json.NewEncoder(etag).Encode(data)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", etag.Sum(nil)), nil
}

// EtagCheck validates the hash of the current state with the hash
// provided by the client
func EtagCheck(r *http.Request, data interface{}) error {
	match := r.Header.Get("If-Match")
	if match == "" {
		return nil
	}

	hash, err := EtagHash(data)
	if err != nil {
		return err
	}

	if hash != match {
		return fmt.Errorf("ETag doesn't match: %s vs %s", hash, match)
	}

	return nil
}

// HTTPClient returns an http.Client using the given certificate and proxy.
func HTTPClient(certificate string, proxy proxyFunc) (*http.Client, error) {
	var err error
	var cert *x509.Certificate

	if certificate != "" {
		certBlock, _ := pem.Decode([]byte(certificate))
		if certBlock == nil {
			return nil, fmt.Errorf("Invalid certificate")
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	tlsConfig, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		TLSClientConfig:   tlsConfig,
		Dial:              shared.RFC3493Dialer,
		Proxy:             proxy,
		DisableKeepAlives: true,
	}

	myhttp := http.Client{
		Transport: tr,
	}

	// Setup redirect policy
	myhttp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return &myhttp, nil
}

// A function capable of proxing an HTTP request.
type proxyFunc func(req *http.Request) (*url.URL, error)

// ContextAwareRequest is an interface implemented by http.Request starting
// from Go 1.8. It supports graceful cancellation using a context.
type ContextAwareRequest interface {
	WithContext(ctx context.Context) *http.Request
}

// CheckTrustState checks whether the given client certificate is trusted
// (i.e. it has a valid time span and it belongs to the given list of trusted
// certificates).
func CheckTrustState(cert x509.Certificate, trustedCerts []x509.Certificate) bool {
	// Extra validity check (should have been caught by TLS stack)
	if time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
		return false
	}

	for k, v := range trustedCerts {
		if bytes.Compare(cert.Raw, v.Raw) == 0 {
			logger.Debug("Found cert", log.Ctx{"k": k})
			return true
		}
	}

	return false
}

// IsRecursionRequest checks whether the given HTTP request is marked with the
// "recursion" flag in its form values.
func IsRecursionRequest(r *http.Request) bool {
	recursionStr := r.FormValue("recursion")

	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		return false
	}

	return recursion != 0
}

// ListenAddresses returns a list of host:port combinations at which
// this machine can be reached
func ListenAddresses(value string) ([]string, error) {
	addresses := make([]string, 0)

	if value == "" {
		return addresses, nil
	}

	localHost, localPort, err := net.SplitHostPort(value)
	if err != nil {
		localHost = value
		localPort = shared.DefaultPort
	}

	if localHost == "0.0.0.0" || localHost == "::" || localHost == "[::]" {
		ifaces, err := net.Interfaces()
		if err != nil {
			return addresses, err
		}

		for _, i := range ifaces {
			addrs, err := i.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}

				if !ip.IsGlobalUnicast() {
					continue
				}

				if ip.To4() == nil {
					if localHost == "0.0.0.0" {
						continue
					}
					addresses = append(addresses, fmt.Sprintf("[%s]:%s", ip, localPort))
				} else {
					addresses = append(addresses, fmt.Sprintf("%s:%s", ip, localPort))
				}
			}
		}
	} else {
		if strings.Contains(localHost, ":") {
			addresses = append(addresses, fmt.Sprintf("[%s]:%s", localHost, localPort))
		} else {
			addresses = append(addresses, fmt.Sprintf("%s:%s", localHost, localPort))
		}
	}

	return addresses, nil
}

// GetListeners returns the socket-activated network listeners, if any.
//
// The 'start' parameter must be SystemdListenFDsStart, except in unit tests,
// see the docstring of SystemdListenFDsStart below.
func GetListeners(start int) []net.Listener {
	defer func() {
		os.Unsetenv("LISTEN_PID")
		os.Unsetenv("LISTEN_FDS")
	}()

	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err != nil {
		return nil
	}

	if pid != os.Getpid() {
		return nil
	}

	fds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil {
		return nil
	}

	listeners := []net.Listener{}

	for i := start; i < start+fds; i++ {
		syscall.CloseOnExec(i)

		file := os.NewFile(uintptr(i), fmt.Sprintf("inherited-fd%d", i))
		listener, err := net.FileListener(file)
		if err != nil {
			continue
		}

		listeners = append(listeners, listener)
	}

	return listeners
}

// SystemdListenFDsStart is the number of the first file descriptor that might
// have been opened by systemd when socket activation is enabled. It's always 3
// in real-world usage (i.e. the first file descriptor opened after stdin,
// stdout and stderr), so this constant should always be the value passed to
// GetListeners, except for unit tests.
const SystemdListenFDsStart = 3
