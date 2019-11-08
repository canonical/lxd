package main

import (
	"crypto/x509"
	"flag"
	"log"
	"path/filepath"

	"github.com/lxc/lxd/lxd/vsock"
	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"
)

var tlsClientCertFile = filepath.Join("/", "media", "lxd_config", "server.crt")
var tlsServerCertFile = filepath.Join("/", "media", "lxd_config", "agent.crt")
var tlsServerKeyFile = filepath.Join("/", "media", "lxd_config", "agent.key")

var debug bool

// cert is the only client certificate which is authorized.
var cert *x509.Certificate

func main() {
	flag.BoolVar(&debug, "debug", false, "Enable debug mode")
	flag.Parse()

	l, err := vsock.Listen(8443)
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Failed to listen on vsock"))
	}

	cert, err = shared.ReadCert(tlsClientCertFile)
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Failed to read client certificate"))
	}

	tlsConfig, err := serverTLSConfig()
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Failed to get TLS config"))
	}

	httpServer := restServer(tlsConfig)

	log.Println(httpServer.ServeTLS(networkTLSListener(l, tlsConfig), tlsServerCertFile, tlsServerKeyFile))
}
