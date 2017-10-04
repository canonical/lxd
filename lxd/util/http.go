package util

import (
	"bytes"
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

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

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

// IsTrustedClient checks if the given HTTP request comes from a trusted client
// (i.e. either it's received via Unix socket, or via TLS with a trusted
// certificate).
func IsTrustedClient(r *http.Request, trustedCerts []x509.Certificate) bool {
	if r.RemoteAddr == "@" {
		// Unix socket
		return true
	}

	if r.TLS == nil {
		return false
	}

	for i := range r.TLS.PeerCertificates {
		if checkTrustState(*r.TLS.PeerCertificates[i], trustedCerts) {
			return true
		}
	}

	return false
}

// Check whether the given client certificate is trusted (i.e. it has a valid
// time span and it belongs to the given list of trusted certificates).
func checkTrustState(cert x509.Certificate, trustedCerts []x509.Certificate) bool {
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

	return recursion == 1
}

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

func GetListeners() []net.Listener {
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

	for i := 3; i < 3+fds; i++ {
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
