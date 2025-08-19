// http://golang.org/src/pkg/crypto/tls/generate_cert.go
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package shared

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// KeyPairAndCA returns a CertInfo object with a reference to the key pair and
// (optionally) CA certificate located in the given directory and having the
// given name prefix
//
// The naming conversion for the various files is:
//
// <prefix>.crt -> public key
// <prefix>.key -> private key
// <prefix>.ca -> CA certificate
//
// If no public/private key files are found, a new key pair will be generated
// and saved on disk.
//
// If a CA certificate is found, it will be returned as well as second return
// value (otherwise it will be nil).
func KeyPairAndCA(dir, prefix string, kind CertKind, addHosts bool) (*CertInfo, error) {
	certFilename := filepath.Join(dir, prefix+".crt")
	keyFilename := filepath.Join(dir, prefix+".key")

	// Ensure that the certificate exists, or create a new one if it does
	// not.
	err := FindOrGenCert(certFilename, keyFilename, kind == CertClient, addHosts)
	if err != nil {
		return nil, err
	}

	// Load the certificate.
	keypair, err := tls.LoadX509KeyPair(certFilename, keyFilename)
	if err != nil {
		return nil, err
	}

	// If available, load the CA data as well.
	caFilename := filepath.Join(dir, prefix+".ca")
	var ca *x509.Certificate
	if PathExists(caFilename) {
		ca, err = ReadCert(caFilename)
		if err != nil {
			return nil, err
		}
	}

	crlFilename := filepath.Join(dir, "ca.crl")
	var crl *pkix.CertificateList
	if PathExists(crlFilename) {
		data, err := ioutil.ReadFile(crlFilename)
		if err != nil {
			return nil, err
		}

		crl, err = x509.ParseCRL(data)
		if err != nil {
			return nil, err
		}
	}

	info := &CertInfo{
		keypair: keypair,
		ca:      ca,
		crl:     crl,
	}
	return info, nil
}

// CertInfo captures TLS certificate information about a certain public/private
// keypair and an optional CA certificate and CRL.
//
// Given LXD's support for PKI setups, these two bits of information are
// normally used and passed around together, so this structure helps with that
// (see doc/security.md for more details).
type CertInfo struct {
	keypair tls.Certificate
	ca      *x509.Certificate
	crl     *pkix.CertificateList
}

// KeyPair returns the public/private key pair.
func (c *CertInfo) KeyPair() tls.Certificate {
	return c.keypair
}

// CA returns the CA certificate.
func (c *CertInfo) CA() *x509.Certificate {
	return c.ca
}

// PublicKey is a convenience to encode the underlying public key to ASCII.
func (c *CertInfo) PublicKey() []byte {
	data := c.KeyPair().Certificate[0]
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: data})
}

// PrivateKey is a convenience to encode the underlying private key.
func (c *CertInfo) PrivateKey() []byte {
	ecKey, ok := c.KeyPair().PrivateKey.(*ecdsa.PrivateKey)
	if ok {
		data, err := x509.MarshalECPrivateKey(ecKey)
		if err != nil {
			return nil
		}

		return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: data})
	}

	rsaKey, ok := c.KeyPair().PrivateKey.(*rsa.PrivateKey)
	if ok {
		data := x509.MarshalPKCS1PrivateKey(rsaKey)
		return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: data})
	}

	return nil
}

// Fingerprint returns the fingerprint of the public key.
func (c *CertInfo) Fingerprint() string {
	fingerprint, err := CertFingerprintStr(string(c.PublicKey()))
	// Parsing should never fail, since we generated the cert ourselves,
	// but let's check the error for good measure.
	if err != nil {
		panic("invalid public key material")
	}
	return fingerprint
}

// CRL returns the certificate revocation list.
func (c *CertInfo) CRL() *pkix.CertificateList {
	return c.crl
}

// CertKind defines the kind of certificate to generate from scratch in
// KeyPairAndCA when it's not there.
//
// The two possible kinds are client and server, and they differ in the
// ext-key-usage bitmaps. See GenerateMemCert for more details.
type CertKind int

// Possible kinds of certificates.
const (
	CertClient CertKind = iota
	CertServer
)

// TestingKeyPair returns CertInfo object initialized with a test keypair. It's
// meant to be used only by tests.
func TestingKeyPair() *CertInfo {
	keypair, err := tls.X509KeyPair(testCertPEMBlock, testKeyPEMBlock)
	if err != nil {
		panic(fmt.Sprintf("invalid X509 keypair material: %v", err))
	}
	cert := &CertInfo{
		keypair: keypair,
	}
	return cert
}

// TestingAltKeyPair returns CertInfo object initialized with a test keypair
// which differs from the one returned by TestCertInfo. It's meant to be used
// only by tests.
func TestingAltKeyPair() *CertInfo {
	keypair, err := tls.X509KeyPair(testAltCertPEMBlock, testAltKeyPEMBlock)
	if err != nil {
		panic(fmt.Sprintf("invalid X509 keypair material: %v", err))
	}
	cert := &CertInfo{
		keypair: keypair,
	}
	return cert
}

/*
 * Generate a list of names for which the certificate will be valid.
 * This will include the hostname and ip address
 */
func mynames() ([]string, error) {
	h, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	ret := []string{h, "127.0.0.1/8", "::1/128"}
	return ret, nil
}

// FindOrGenCert generates a keypair if needed.
// The type argument is false for server, true for client.
func FindOrGenCert(certf string, keyf string, certtype bool, addHosts bool) error {
	if PathExists(certf) && PathExists(keyf) {
		return nil
	}

	/* If neither stat succeeded, then this is our first run and we
	 * need to generate cert and privkey */
	err := GenCert(certf, keyf, certtype, addHosts)
	if err != nil {
		return err
	}

	return nil
}

// GenCert will create and populate a certificate file and a key file
func GenCert(certf string, keyf string, certtype bool, addHosts bool) error {
	/* Create the basenames if needed */
	dir := filepath.Dir(certf)
	err := os.MkdirAll(dir, 0750)
	if err != nil {
		return err
	}

	dir = filepath.Dir(keyf)
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		return err
	}

	certBytes, keyBytes, err := GenerateMemCert(certtype, addHosts)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certf)
	if err != nil {
		return fmt.Errorf("Failed to open %s for writing: %v", certf, err)
	}
	certOut.Write(certBytes)
	certOut.Close()

	keyOut, err := os.OpenFile(keyf, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("Failed to open %s for writing: %v", keyf, err)
	}
	keyOut.Write(keyBytes)
	keyOut.Close()
	return nil
}

// GenerateMemCert creates client or server certificate and key pair,
// returning them as byte arrays in memory.
func GenerateMemCert(client bool, addHosts bool) ([]byte, []byte, error) {
	privk, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to generate key: %v", err)
	}

	validFrom := time.Now()
	validTo := validFrom.Add(10 * 365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to generate serial number: %v", err)
	}

	userEntry, err := user.Current()
	var username string
	if err == nil {
		username = userEntry.Username
		if username == "" {
			username = "UNKNOWN"
		}
	} else {
		username = "UNKNOWN"
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "UNKNOWN"
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"linuxcontainers.org"},
			CommonName:   fmt.Sprintf("%s@%s", username, hostname),
		},
		NotBefore: validFrom,
		NotAfter:  validTo,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	if client {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}

	if addHosts {
		hosts, err := mynames()
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to get my hostname: %v", err)
		}

		for _, h := range hosts {
			if ip, _, err := net.ParseCIDR(h); err == nil {
				if !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() {
					template.IPAddresses = append(template.IPAddresses, ip)
				}
			} else {
				template.DNSNames = append(template.DNSNames, h)
			}
		}
	} else if !client {
		template.DNSNames = []string{"unspecified"}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privk.PublicKey, privk)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create certificate: %v", err)
	}

	data, err := x509.MarshalECPrivateKey(privk)
	if err != nil {
		return nil, nil, err
	}

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	key := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: data})

	return cert, key, nil
}

func ReadCert(fpath string) (*x509.Certificate, error) {
	cf, err := ioutil.ReadFile(fpath)
	if err != nil {
		return nil, err
	}

	certBlock, _ := pem.Decode(cf)
	if certBlock == nil {
		return nil, fmt.Errorf("Invalid certificate file")
	}

	return x509.ParseCertificate(certBlock.Bytes)
}

func CertFingerprint(cert *x509.Certificate) string {
	return fmt.Sprintf("%x", sha256.Sum256(cert.Raw))
}

func CertFingerprintStr(c string) (string, error) {
	pemCertificate, _ := pem.Decode([]byte(c))
	if pemCertificate == nil {
		return "", fmt.Errorf("invalid certificate")
	}

	cert, err := x509.ParseCertificate(pemCertificate.Bytes)
	if err != nil {
		return "", err
	}

	return CertFingerprint(cert), nil
}

func GetRemoteCertificate(address string, useragent string) (*x509.Certificate, error) {
	// Setup a permissive TLS config
	tlsConfig, err := GetTLSConfig("", "", "", nil)
	if err != nil {
		return nil, err
	}

	tlsConfig.InsecureSkipVerify = true

	// Support disabling of strict ciphers
	if IsTrue(os.Getenv("LXD_INSECURE_TLS")) {
		tlsConfig.CipherSuites = nil
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
		Dial:            RFC3493Dialer,
		Proxy:           ProxyFromEnvironment,
	}

	// Connect
	req, err := http.NewRequest("GET", address, nil)
	if err != nil {
		return nil, err
	}

	if useragent != "" {
		req.Header.Set("User-Agent", useragent)
	}

	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Retrieve the certificate
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return nil, fmt.Errorf("Unable to read remote TLS certificate")
	}

	return resp.TLS.PeerCertificates[0], nil
}

var testCertPEMBlock = []byte(`-----BEGIN CERTIFICATE-----
MIIB3jCCAWSgAwIBAgIRAJhyjO6BDDQQG7vHC2feJukwCgYIKoZIzj0EAwMwIjEM
MAoGA1UEChMDTFhEMRIwEAYDVQQDDAlyb290QHRlc3QwHhcNMjMxMDI2MjAxMTIx
WhcNMzMxMDIzMjAxMTIxWjAiMQwwCgYDVQQKEwNMWEQxEjAQBgNVBAMMCXJvb3RA
dGVzdDB2MBAGByqGSM49AgEGBSuBBAAiA2IABLCmWlzKcLbr1OA662fMh1WMwjPx
v4gNLlzyxYyMM2BJVHOHNDEZFa5vhHX7KDVswKMheScr9zUIbDX8mWrb6FiuNw1A
geE1/7xTf8VXNpruI5esIWSK05H1u3fz2Xfc56NeMFwwDgYDVR0PAQH/BAQDAgWg
MBMGA1UdJQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwJwYDVR0RBCAwHoIE
dGVzdIcEfwAAAYcQAAAAAAAAAAAAAAAAAAAAATAKBggqhkjOPQQDAwNoADBlAjEA
qkldezumODusc3T2xi0tdn386eYi5LPbQtYes4OmVP7dMwQjH181gIBpN33w0zWo
AjBLmqiJyxnXQqoBInc04XD7H8+V4L92V4btjJdbc+tKPepbod0Eahbm3+2VmEVa
TXg=
-----END CERTIFICATE-----
`)

var testKeyPEMBlock = []byte(`-----BEGIN EC PRIVATE KEY-----
MIGkAgEBBDAtiHLkkFqmLsvH1n1g747qUgGGvmpjI7orogy4Mm1ZqlumO6Ozymc4
+9mHDYxMaoWgBwYFK4EEACKhZANiAASwplpcynC269TgOutnzIdVjMIz8b+IDS5c
8sWMjDNgSVRzhzQxGRWub4R1+yg1bMCjIXknK/c1CGw1/Jlq2+hYrjcNQIHhNf+8
U3/FVzaa7iOXrCFkitOR9bt389l33Oc=
-----END EC PRIVATE KEY-----
`)

var testAltCertPEMBlock = []byte(`-----BEGIN CERTIFICATE-----
MIICEzCCAXygAwIBAgIQMIMChMLGrR+QvmQvpwAU6zANBgkqhkiG9w0BAQsFADAS
MRAwDgYDVQQKEwdBY21lIENvMCAXDTcwMDEwMTAwMDAwMFoYDzIwODQwMTI5MTYw
MDAwWjASMRAwDgYDVQQKEwdBY21lIENvMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCB
iQKBgQDuLnQAI3mDgey3VBzWnB2L39JUU4txjeVE6myuDqkM/uGlfjb9SjY1bIw4
iA5sBBZzHi3z0h1YV8QPuxEbi4nW91IJm2gsvvZhIrCHS3l6afab4pZBl2+XsDul
rKBxKKtD1rGxlG4LjncdabFn9gvLZad2bSysqz/qTAUStTvqJQIDAQABo2gwZjAO
BgNVHQ8BAf8EBAMCAqQwEwYDVR0lBAwwCgYIKwYBBQUHAwEwDwYDVR0TAQH/BAUw
AwEB/zAuBgNVHREEJzAlggtleGFtcGxlLmNvbYcEfwAAAYcQAAAAAAAAAAAAAAAA
AAAAATANBgkqhkiG9w0BAQsFAAOBgQCEcetwO59EWk7WiJsG4x8SY+UIAA+flUI9
tyC4lNhbcF2Idq9greZwbYCqTTTr2XiRNSMLCOjKyI7ukPoPjo16ocHj+P3vZGfs
h1fIw3cSS2OolhloGw/XM6RWPWtPAlGykKLciQrBru5NAPvCMsb/I1DAceTiotQM
fblo6RBxUQ==
-----END CERTIFICATE-----`)

var testAltKeyPEMBlock = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIICXgIBAAKBgQDuLnQAI3mDgey3VBzWnB2L39JUU4txjeVE6myuDqkM/uGlfjb9
SjY1bIw4iA5sBBZzHi3z0h1YV8QPuxEbi4nW91IJm2gsvvZhIrCHS3l6afab4pZB
l2+XsDulrKBxKKtD1rGxlG4LjncdabFn9gvLZad2bSysqz/qTAUStTvqJQIDAQAB
AoGAGRzwwir7XvBOAy5tM/uV6e+Zf6anZzus1s1Y1ClbjbE6HXbnWWF/wbZGOpet
3Zm4vD6MXc7jpTLryzTQIvVdfQbRc6+MUVeLKwZatTXtdZrhu+Jk7hx0nTPy8Jcb
uJqFk541aEw+mMogY/xEcfbWd6IOkp+4xqjlFLBEDytgbIECQQDvH/E6nk+hgN4H
qzzVtxxr397vWrjrIgPbJpQvBsafG7b0dA4AFjwVbFLmQcj2PprIMmPcQrooz8vp
jy4SHEg1AkEA/v13/5M47K9vCxmb8QeD/asydfsgS5TeuNi8DoUBEmiSJwma7FXY
fFUtxuvL7XvjwjN5B30pNEbc6Iuyt7y4MQJBAIt21su4b3sjXNueLKH85Q+phy2U
fQtuUE9txblTu14q3N7gHRZB4ZMhFYyDy8CKrN2cPg/Fvyt0Xlp/DoCzjA0CQQDU
y2ptGsuSmgUtWj3NM9xuwYPm+Z/F84K6+ARYiZ6PYj013sovGKUFfYAqVXVlxtIX
qyUBnu3X9ps8ZfjLZO7BAkEAlT4R5Yl6cGhaJQYZHOde3JEMhNRcVFMO8dJDaFeo
f9Oeos0UUothgiDktdQHxdNEwLjQf7lJJBzV+5OtwswCWA==
-----END RSA PRIVATE KEY-----`)
