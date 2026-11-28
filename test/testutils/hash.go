package testutils

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// GetSizeAndSHA256HashFromFile opens the file at the given path and calls [GetSizeAndSHA256Hash] with it (then closes it).
func GetSizeAndSHA256HashFromFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}

	defer f.Close()

	return GetSizeAndSHA256Hash(f)
}

// GetSizeAndSHA256Hash hashes the contents of the [io.Reader] and returns that and the number of read bytes.
func GetSizeAndSHA256Hash(reader io.Reader) (int64, string, error) {
	hasher := sha256.New()
	size, err := io.Copy(hasher, reader)
	if err != nil {
		return 0, "", err
	}

	hashInBytes := hasher.Sum(nil)
	return size, hex.EncodeToString(hashInBytes), nil
}
