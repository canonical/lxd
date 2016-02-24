package shared

import (
	"encoding/pem"
	"testing"
)

func TestGenerateMemCert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cert generation in short mode")
	}
	cert, key, err := GenerateMemCert()
	if err != nil {
		t.Error(err)
		return
	}
	if cert == nil {
		t.Error("GenerateMemCert returned a nil cert")
		return
	}
	if key == nil {
		t.Error("GenerateMemCert returned a nil key")
		return
	}
	block, rest := pem.Decode(cert)
	if len(rest) != 0 {
		t.Errorf("GenerateMemCert returned a cert with trailing content: %q", string(rest))
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("GenerateMemCert returned a cert with Type %q not \"CERTIFICATE\"", block.Type)
	}
	block, rest = pem.Decode(key)
	if len(rest) != 0 {
		t.Errorf("GenerateMemCert returned a key with trailing content: %q", string(rest))
	}
	if block.Type != "RSA PRIVATE KEY" {
		t.Errorf("GenerateMemCert returned a cert with Type %q not \"RSA PRIVATE KEY\"", block.Type)
	}
}
