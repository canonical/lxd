package util

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/scrypt"
)

func PasswordCheck(secret, password string) error {
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
