package lxd

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
)

func ParseLXDFileHeaders(headers http.Header) (uid int, gid int, mode os.FileMode, err error) {

	uid, err = strconv.Atoi(headers.Get("X-LXD-uid"))
	if err != nil {
		return 0, 0, 0, err
	}

	gid, err = strconv.Atoi(headers.Get("X-LXD-gid"))
	if err != nil {
		return 0, 0, 0, err
	}

	/* Allow people to send stuff with a leading 0 for octal or a regular
	 * int that represents the perms when redered in octal. */
	rawMode, err := strconv.ParseInt(headers.Get("X-LXD-mode"), 0, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	mode = os.FileMode(rawMode)

	return uid, gid, mode, nil
}

func ReadToJson(r io.Reader, req interface{}) error {

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	return json.Unmarshal(buf, req)
}

func GenerateFingerprint(cert *x509.Certificate) string {
	return fmt.Sprintf("% x", sha256.Sum256(cert.Raw))
}
