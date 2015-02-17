// http://golang.org/src/pkg/crypto/tls/generate_cert.go
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package shared

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path"
	"strings"
	"time"
)

/*
 * Generate a list of names for which the certificate will be valid.
 * This will include the hostname and ip address
 */
func mynames() (*string, error) {
	h, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	// TODO - add InterfaceAddrs to this, comma-separated
	return &h, nil
}

func FindOrGenCert(certf string, keyf string) error {
	_, err := os.Stat(certf)
	_, err2 := os.Stat(keyf)

	/*
	 * If both stat's succeeded, then the cert and pubkey already
	 * exist.
	 */
	if err == nil && err2 == nil {
		return nil
	}

	/* If one of the stats succeeded and one failed, then there's
	 * a configuration problem, return an error */
	if err == nil {
		return err2
	}
	if err2 == nil {
		return err
	}

	/* If neither stat succeeded, then this is our first run and we
	 * need to generate cert and privkey */
	err = GenCert(certf, keyf)
	if err != nil {
		return err
	}

	return nil
}

func GenCert(certf string, keyf string) error {
	privk, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatalf("failed to generate key")
		return err
	}

	/* Create the basenames if needed */
	dir := path.Dir(certf)
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		return err
	}
	dir = path.Dir(keyf)
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		return err
	}

	names, err := mynames()
	if err != nil {
		log.Fatalf("Failed to get my hostname")
		return err
	}

	validFrom := time.Now()
	validTo := validFrom.Add(365 * 24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Fatalf("failed to generate serial number: %s", err)
		return err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"linuxcontainer.org"},
		},
		NotBefore: validFrom,
		NotAfter:  validTo,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	hosts := strings.Split(*names, ",")
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privk.PublicKey, privk)
	if err != nil {
		log.Fatalf("Failed to create certificate: %s", err)
		return err
	}

	certOut, err := os.Create(certf)
	if err != nil {
		log.Fatalf("failed to open %s for writing: %s", certf, err)
		return err
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()

	keyOut, err := os.OpenFile(keyf, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Printf("failed to open %s for writing: %s", keyf, err)
		return err
	}
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privk)})
	keyOut.Close()
	return nil
}
