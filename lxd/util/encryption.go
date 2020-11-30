package util

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/lxc/lxd/shared"
	"github.com/pkg/errors"

	"golang.org/x/crypto/scrypt"
)

// PasswordCheck validates the provided password against the encoded secret
func PasswordCheck(secret string, password string) error {
	// No password set
	if secret == "" {
		return fmt.Errorf("No password is set")
	}

	// Compare the password
	buff, err := hex.DecodeString(secret)
	if err != nil {
		return err
	}

	salt := buff[0:32]
	hash, err := scrypt.Key([]byte(password), salt, 1<<14, 8, 1, 64)
	if err != nil {
		return err
	}

	if !bytes.Equal(hash, buff[32:]) {
		return fmt.Errorf("Bad password provided")
	}

	return nil
}

// LoadCert reads the LXD server certificate from the given var dir.
//
// If a cluster certificate is found it will be loaded instead.
func LoadCert(dir string) (*shared.CertInfo, error) {
	prefix := "server"
	if shared.PathExists(filepath.Join(dir, "cluster.crt")) {
		prefix = "cluster"
	}

	cert, err := shared.KeyPairAndCA(dir, prefix, shared.CertServer, true)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load TLS certificate")
	}

	return cert, nil
}

// WriteCert writes the given material to the appropriate certificate files in
// the given LXD var directory.
func WriteCert(dir, prefix string, cert, key, ca []byte) error {
	err := ioutil.WriteFile(filepath.Join(dir, prefix+".crt"), cert, 0644)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(dir, prefix+".key"), key, 0600)
	if err != nil {
		return err
	}

	if ca != nil {
		err = ioutil.WriteFile(filepath.Join(dir, prefix+".ca"), ca, 0644)
		if err != nil {
			return err
		}
	}

	return nil
}
