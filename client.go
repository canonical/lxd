package lxd

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/chai2010/gettext-go/gettext"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh/terminal"

	"github.com/lxc/lxd/shared"
)

// Client can talk to a LXD daemon.
type Client struct {
	config          Config
	Remote          *RemoteConfig
	name            string
	http            http.Client
	BaseURL         string
	BaseWSURL       string
	certf           string
	keyf            string
	websocketDialer websocket.Dialer

	scert *x509.Certificate // the cert stored on disk

	scertWire          *x509.Certificate // the cert from the tls connection
	scertIntermediates *x509.CertPool
	scertDigest        [sha256.Size]byte // fingerprint of server cert from connection
	scertDigestSet     bool              // whether we've stored the fingerprint
}

type ResponseType string

const (
	Sync  ResponseType = "sync"
	Async ResponseType = "async"
	Error ResponseType = "error"
)

var (
	// LXDErrors are special errors; the client library hoists error codes
	// to these errors internally so that user code can compare against
	// them. We probably shouldn't hoist BadRequest or InternalError, since
	// LXD passes an error string along which is more informative than
	// whatever static error message we would put here.
	LXDErrors = map[int]error{
		http.StatusNotFound: fmt.Errorf("not found"),
	}
)

type Response struct {
	Type ResponseType `json:"type"`

	/* Valid only for Sync responses */
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`

	/* Valid only for Async responses */
	Operation string              `json:"operation"`
	Resources map[string][]string `json:"resources"`

	/* Valid only for Error responses */
	Code  int    `json:"error_code"`
	Error string `json:"error"`

	/* Valid for Sync and Error responses */
	Metadata json.RawMessage `json:"metadata"`
}

func (r *Response) MetadataAsMap() (*shared.Jmap, error) {
	ret := shared.Jmap{}
	if err := json.Unmarshal(r.Metadata, &ret); err != nil {
		return nil, err
	}
	return &ret, nil
}

func (r *Response) MetadataAsOperation() (*shared.Operation, error) {
	op := shared.Operation{}
	if err := json.Unmarshal(r.Metadata, &op); err != nil {
		return nil, err
	}

	return &op, nil
}

// ParseResponse parses a lxd style response out of an http.Response. Note that
// this does _not_ automatically convert error responses to golang errors. To
// do that, use ParseError. Internal client library uses should probably use
// HoistResponse, unless they are interested in accessing the underlying Error
// response (e.g. to inspect the error code).
func ParseResponse(r *http.Response) (*Response, error) {
	if r == nil {
		return nil, fmt.Errorf(gettext.Gettext("no response!"))
	}
	defer r.Body.Close()
	ret := Response{}

	s, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	shared.Debugf("raw response: %s", string(s))

	if err := json.Unmarshal(s, &ret); err != nil {
		return nil, err
	}

	return &ret, nil
}

// HoistResponse hoists a regular http response into a response of type rtype
// or returns a golang error.
func HoistResponse(r *http.Response, rtype ResponseType) (*Response, error) {
	resp, err := ParseResponse(r)
	if err != nil {
		return nil, err
	}

	if resp.Type == Error {
		// Try and use a known error if we have one for this code.
		err, ok := LXDErrors[resp.Code]
		if !ok {
			return nil, fmt.Errorf(resp.Error)
		}
		return nil, err
	}

	if resp.Type != rtype {
		return nil, fmt.Errorf(gettext.Gettext("got bad response type, expected %s got %s"), rtype, resp.Type)
	}

	return resp, nil
}

func readMyCert() (string, string, error) {
	certf := ConfigPath("client.crt")
	keyf := ConfigPath("client.key")

	err := shared.FindOrGenCert(certf, keyf)

	return certf, keyf, err
}

/*
 * load the server cert from disk
 */
func (c *Client) loadServerCert() {
	cert, err := shared.ReadCert(ServerCertPath(c.name))
	if err != nil {
		shared.Debugf("Error reading the server certificate for %s: %v\n", c.name, err)
		return
	}

	c.scert = cert
}

// NewClient returns a new LXD client.
func NewClient(config *Config, remote string) (*Client, error) {
	c := Client{
		config: *config,
		http:   http.Client{},
	}

	c.name = remote

	// TODO: Here, we don't support configurable local remotes, we only
	// support the default local LXD at /var/lib/lxd/unix.socket.
	if remote == "" {
		c.BaseURL = "http://unix.socket"
		c.BaseWSURL = "ws://unix.socket"
		c.http.Transport = &unixTransport
		c.websocketDialer.NetDial = unixDial
	} else if r, ok := config.Remotes[remote]; ok {
		if r.Addr[0:5] == "unix:" {
			c.BaseURL = "http://unix.socket"
			c.BaseWSURL = "ws://unix.socket"
			uDial := func(networ, addr string) (net.Conn, error) {
				var err error
				var raddr *net.UnixAddr
				if r.Addr[7:] == "unix://" {
					raddr, err = net.ResolveUnixAddr("unix", r.Addr[7:])
				} else {
					raddr, err = net.ResolveUnixAddr("unix", r.Addr[5:])
				}
				if err != nil {
					return nil, err
				}
				return net.DialUnix("unix", nil, raddr)
			}
			c.http.Transport = &http.Transport{Dial: uDial}
			c.websocketDialer.NetDial = uDial
			c.Remote = &r
			return &c, nil
		} else {
			certf, keyf, err := readMyCert()
			if err != nil {
				return nil, err
			}

			tlsconfig, err := shared.GetTLSConfig(certf, keyf)
			if err != nil {
				return nil, err
			}

			tr := &http.Transport{
				TLSClientConfig: tlsconfig,
				Dial:            shared.RFC3493Dialer,
				Proxy:           http.ProxyFromEnvironment,
			}

			c.websocketDialer = websocket.Dialer{
				NetDial:         shared.RFC3493Dialer,
				TLSClientConfig: tlsconfig,
			}

			c.certf = certf
			c.keyf = keyf

			if r.Addr[0:8] == "https://" {
				c.BaseURL = "https://" + r.Addr[8:]
				c.BaseWSURL = "wss://" + r.Addr[8:]
			} else {
				c.BaseURL = "https://" + r.Addr
				c.BaseWSURL = "wss://" + r.Addr
			}
			c.http.Transport = tr
			c.loadServerCert()
			c.Remote = &r
		}
	} else {
		return nil, fmt.Errorf(gettext.Gettext("unknown remote name: %q"), remote)
	}
	if err := c.Finger(); err != nil {
		return nil, err
	}

	return &c, nil
}

func (c *Client) get(base string) (*Response, error) {
	uri := c.url(shared.APIVersion, base)

	return c.baseGet(uri)
}

func (c *Client) baseGet(getUrl string) (*Response, error) {
	req, err := http.NewRequest("GET", getUrl, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", shared.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if c.scert != nil && resp.TLS != nil {
		if !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, c.scert.Raw) {
			pUrl, _ := url.Parse(getUrl)
			return nil, fmt.Errorf(gettext.Gettext("Server certificate for host %s has changed. Add correct certificate or remove certificate in %s"), pUrl.Host, ConfigPath("servercerts"))
		}
	}

	if c.scertDigestSet == false && resp.TLS != nil {
		c.scertWire = resp.TLS.PeerCertificates[0]
		c.scertIntermediates = x509.NewCertPool()
		for _, cert := range resp.TLS.PeerCertificates {
			c.scertIntermediates.AddCert(cert)
		}
		c.scertDigest = sha256.Sum256(resp.TLS.PeerCertificates[0].Raw)
		c.scertDigestSet = true
	}

	return HoistResponse(resp, Sync)
}

func (c *Client) put(base string, args shared.Jmap, rtype ResponseType) (*Response, error) {
	uri := c.url(shared.APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	shared.Debugf("putting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("PUT", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	return HoistResponse(resp, rtype)
}

func (c *Client) post(base string, args shared.Jmap, rtype ResponseType) (*Response, error) {
	uri := c.url(shared.APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	shared.Debugf("posting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("POST", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	return HoistResponse(resp, rtype)
}

func (c *Client) getRaw(uri string) (*http.Response, error) {
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)

	raw, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	// because it is raw data, we need to check for http status
	if raw.StatusCode != 200 {
		resp, err := HoistResponse(raw, Sync)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf(gettext.Gettext("expected error, got %s"), resp)
	}

	return raw, nil
}

func (c *Client) delete(base string, args shared.Jmap, rtype ResponseType) (*Response, error) {
	uri := c.url(shared.APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	shared.Debugf("deleting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("DELETE", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	return HoistResponse(resp, rtype)
}

func (c *Client) websocket(operation string, secret string) (*websocket.Conn, error) {
	query := url.Values{"secret": []string{secret}}
	url := c.BaseWSURL + path.Join(operation, "websocket") + "?" + query.Encode()
	return WebsocketDial(c.websocketDialer, url)
}

func (c *Client) url(elem ...string) string {
	return c.BaseURL + "/" + path.Join(elem...)
}

func unixDial(networ, addr string) (net.Conn, error) {
	var raddr *net.UnixAddr
	var err error
	if addr == "unix.socket:80" {
		raddr, err = net.ResolveUnixAddr("unix", shared.VarPath("unix.socket"))
		if err != nil {
			return nil, fmt.Errorf(gettext.Gettext("cannot resolve unix socket address: %v"), err)
		}
	} else { // TODO - I think this is dead code
		raddr, err = net.ResolveUnixAddr("unix", addr)
		if err != nil {
			return nil, fmt.Errorf(gettext.Gettext("cannot resolve unix socket address: %v"), err)
		}
	}
	return net.DialUnix("unix", nil, raddr)
}

var unixTransport = http.Transport{
	Dial: unixDial,
}

func (c *Client) GetServerConfig() (*Response, error) {
	return c.baseGet(c.url(shared.APIVersion))
}

func (c *Client) Finger() error {
	shared.Debugf("fingering the daemon")
	resp, err := c.GetServerConfig()
	if err != nil {
		return err
	}

	jmap, err := resp.MetadataAsMap()
	if err != nil {
		return err
	}

	serverAPICompat, err := jmap.GetInt("api_compat")
	if err != nil {
		return err
	}

	if serverAPICompat != shared.APICompat {
		return fmt.Errorf(gettext.Gettext("api version mismatch: mine: %q, daemon: %q"), shared.APICompat, serverAPICompat)
	}
	shared.Debugf("pong received")
	return nil
}

func (c *Client) AmTrusted() bool {
	resp, err := c.GetServerConfig()
	if err != nil {
		return false
	}

	shared.Debugf("%s", resp)

	jmap, err := resp.MetadataAsMap()
	if err != nil {
		return false
	}

	auth, err := jmap.GetString("auth")
	if err != nil {
		return false
	}

	return auth == "trusted"
}

func (c *Client) ListContainers() ([]shared.ContainerInfo, error) {
	resp, err := c.get("containers?recursion=1")
	if err != nil {
		return nil, err
	}

	var result []shared.ContainerInfo

	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *Client) CopyImage(image string, dest *Client, copy_aliases bool, aliases []string, public bool) error {
	fingerprint := c.GetAlias(image)
	if fingerprint == "" {
		fingerprint = image
	}

	info, err := c.GetImageInfo(fingerprint)
	if err != nil {
		return err
	}

	source := shared.Jmap{
		"type":        "image",
		"mode":        "pull",
		"server":      c.BaseURL,
		"fingerprint": fingerprint}

	if info.Public == 0 {
		var operation string

		resp, err := c.post("images/"+fingerprint+"/secret", nil, Async)
		if err != nil {
			return err
		}

		toScan := strings.Replace(resp.Operation, "/", " ", -1)
		version := ""
		count, err := fmt.Sscanf(toScan, " %s operations %s", &version, &operation)
		if err != nil || count != 2 {
			return err
		}

		md := secretMd{}
		if err := json.Unmarshal(resp.Metadata, &md); err != nil {
			return err
		}

		source["secret"] = md.Secret
	}

	body := shared.Jmap{"public": public, "source": source}

	_, err = dest.post("images", body, Sync)
	if err != nil {
		return err
	}

	/* copy aliases from source image */
	if copy_aliases {
		for _, alias := range info.Aliases {
			dest.DeleteAlias(alias.Name)
			err = dest.PostAlias(alias.Name, alias.Description, info.Fingerprint)
			if err != nil {
				fmt.Printf(gettext.Gettext("Error adding alias %s\n"), alias.Name)
			}
		}
	}

	/* add new aliases */
	for _, alias := range aliases {
		dest.DeleteAlias(alias)
		err = dest.PostAlias(alias, alias, info.Fingerprint)
		if err != nil {
			fmt.Printf(gettext.Gettext("Error adding alias %s\n"), alias)
		}
	}

	return nil
}

func (c *Client) ExportImage(image string, target string) (*Response, string, error) {
	uri := c.url(shared.APIVersion, "images", image, "export")
	raw, err := c.getRaw(uri)
	if err != nil {
		return nil, "", err
	}

	ctype, ctypeParams, err := mime.ParseMediaType(raw.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	// Deal with split images
	if ctype == "multipart/form-data" {
		if !shared.IsDir(target) {
			return nil, "", fmt.Errorf(gettext.Gettext("Split images can only be written to a directory."))
		}

		// Parse the POST data
		mr := multipart.NewReader(raw.Body, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			return nil, "", err
		}

		if part.FormName() != "metadata" {
			return nil, "", fmt.Errorf("Invalid multipart image")
		}

		imageTarf, err := os.OpenFile(part.FileName(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return nil, "", err
		}

		_, err = io.Copy(imageTarf, part)

		imageTarf.Close()
		if err != nil {
			return nil, "", err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			return nil, "", err
		}

		if part.FormName() != "rootfs" {
			return nil, "", fmt.Errorf("Invalid multipart image")
		}

		rootfsTarf, err := os.OpenFile(part.FileName(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return nil, "", err
		}

		_, err = io.Copy(rootfsTarf, part)

		rootfsTarf.Close()
		if err != nil {
			return nil, "", err
		}

		return nil, target, nil
	}

	// Deal with unified images
	var wr io.Writer
	var destpath string
	if target == "-" {
		wr = os.Stdout
		destpath = "stdout"
	} else if fi, err := os.Stat(target); err == nil {
		// file exists, so check if folder
		switch mode := fi.Mode(); {
		case mode.IsDir():
			// save in directory, header content-disposition can not be null
			// and will have a filename
			cd := strings.Split(raw.Header["Content-Disposition"][0], "=")

			// write filename from header
			destpath = filepath.Join(target, cd[1])
			f, err := os.Create(destpath)
			defer f.Close()

			if err != nil {
				return nil, "", err
			}

			wr = f

		default:
			// overwrite file
			destpath = target
			f, err := os.OpenFile(destpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
			defer f.Close()

			if err != nil {
				return nil, "", err
			}

			wr = f
		}

	} else {

		// write as simple file
		destpath = target
		f, err := os.Create(destpath)
		defer f.Close()

		wr = f
		if err != nil {
			return nil, "", err
		}

	}

	_, err = io.Copy(wr, raw.Body)

	if err != nil {
		return nil, "", err
	}

	// it streams to stdout or file, so no response returned
	return nil, destpath, nil
}

func (c *Client) PostImage(imageFile string, rootfsFile string, properties []string, public bool, aliases []string) (string, error) {
	uri := c.url(shared.APIVersion, "images")

	var err error
	var fImage *os.File
	var fRootfs *os.File
	var req *http.Request

	fImage, err = os.Open(imageFile)
	if err != nil {
		return "", err
	}
	defer fImage.Close()

	if rootfsFile != "" {
		fRootfs, err = os.Open(rootfsFile)
		if err != nil {
			return "", err
		}
		defer fRootfs.Close()

		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)

		// Metadata file
		fw, err := w.CreateFormFile("metadata", path.Base(imageFile))
		if err != nil {
			return "", err
		}

		_, err = io.Copy(fw, fImage)
		if err != nil {
			return "", err
		}

		// Rootfs file
		fw, err = w.CreateFormFile("rootfs", path.Base(rootfsFile))
		if err != nil {
			return "", err
		}

		_, err = io.Copy(fw, fRootfs)
		if err != nil {
			return "", err
		}

		w.Close()

		req, err = http.NewRequest("POST", uri, body)
		req.Header.Set("Content-Type", w.FormDataContentType())
	} else {
		req, err = http.NewRequest("POST", uri, fImage)
		req.Header.Set("X-LXD-filename", filepath.Base(imageFile))
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", shared.UserAgent)

	if public {
		req.Header.Set("X-LXD-public", "1")
	} else {
		req.Header.Set("X-LXD-public", "0")
	}

	if len(properties) != 0 {
		imgProps := url.Values{}
		for _, value := range properties {
			eqIndex := strings.Index(value, "=")

			// props must be in key=value format
			// if not, request will not be accepted
			if eqIndex > -1 {
				imgProps.Set(value[:eqIndex], value[eqIndex+1:])
			} else {
				return "", fmt.Errorf(gettext.Gettext("Bad image property: %s\n"), value)
			}

		}

		req.Header.Set("X-LXD-properties", imgProps.Encode())
	}

	raw, err := c.http.Do(req)
	if err != nil {
		return "", err
	}

	resp, err := HoistResponse(raw, Sync)
	if err != nil {
		return "", err
	}

	jmap, err := resp.MetadataAsMap()
	if err != nil {
		return "", err
	}

	fingerprint, err := jmap.GetString("fingerprint")
	if err != nil {
		return "", err
	}

	/* add new aliases */
	for _, alias := range aliases {
		c.DeleteAlias(alias)
		err = c.PostAlias(alias, alias, fingerprint)
		if err != nil {
			fmt.Printf(gettext.Gettext("Error adding alias %s\n"), alias)
		}
	}

	return fingerprint, nil
}

func (c *Client) GetImageInfo(image string) (*shared.ImageInfo, error) {
	resp, err := c.get(fmt.Sprintf("images/%s", image))
	if err != nil {
		return nil, err
	}

	info := shared.ImageInfo{}
	if err := json.Unmarshal(resp.Metadata, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

func (c *Client) PutImageProperties(name string, p shared.ImageProperties) error {
	body := shared.Jmap{"properties": p}
	_, err := c.put(fmt.Sprintf("images/%s", name), body, Sync)
	return err
}

func (c *Client) ListImages() ([]shared.ImageInfo, error) {
	resp, err := c.get("images?recursion=1")
	if err != nil {
		return nil, err
	}

	var result []shared.ImageInfo
	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *Client) DeleteImage(image string) error {
	_, err := c.delete(fmt.Sprintf("images/%s", image), nil, Sync)
	return err
}

func (c *Client) PostAlias(alias string, desc string, target string) error {
	body := shared.Jmap{"description": desc, "target": target, "name": alias}

	_, err := c.post("images/aliases", body, Sync)
	return err
}

func (c *Client) DeleteAlias(alias string) error {
	_, err := c.delete(fmt.Sprintf("images/aliases/%s", alias), nil, Sync)
	return err
}

func (c *Client) ListAliases() ([]shared.ImageAlias, error) {
	resp, err := c.get("images/aliases?recursion=1")
	if err != nil {
		return nil, err
	}

	var result []shared.ImageAlias

	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *Client) UserAuthServerCert(name string, acceptCert bool) error {
	if !c.scertDigestSet {
		return fmt.Errorf(gettext.Gettext("No certificate on this connection"))
	}

	if c.scert != nil {
		return nil
	}

	_, err := c.scertWire.Verify(x509.VerifyOptions{
		DNSName:       name,
		Intermediates: c.scertIntermediates,
	})
	if err != nil {
		if acceptCert == false {
			fmt.Printf(gettext.Gettext("Certificate fingerprint: % x\n"), c.scertDigest)
			fmt.Printf(gettext.Gettext("ok (y/n)? "))
			line, err := shared.ReadStdin()
			if err != nil {
				return err
			}
			if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
				return fmt.Errorf(gettext.Gettext("Server certificate NACKed by user"))
			}
		}
	}

	// User acked the cert, now add it to our store
	dnam := ConfigPath("servercerts")
	err = os.MkdirAll(dnam, 0750)
	if err != nil {
		return fmt.Errorf(gettext.Gettext("Could not create server cert dir"))
	}
	certf := fmt.Sprintf("%s/%s.crt", dnam, c.name)
	certOut, err := os.Create(certf)
	if err != nil {
		return err
	}

	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: c.scertWire.Raw})

	certOut.Close()
	return err
}

func (c *Client) CertificateList() ([]shared.CertInfo, error) {
	resp, err := c.get("certificates?recursion=1")
	if err != nil {
		return nil, err
	}

	var result []shared.CertInfo
	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *Client) AddMyCertToServer(pwd string) error {
	body := shared.Jmap{"type": "client", "password": pwd}

	_, err := c.post("certificates", body, Sync)
	return err
}

func (c *Client) CertificateAdd(cert *x509.Certificate, name string) error {
	b64 := base64.StdEncoding.EncodeToString(cert.Raw)
	_, err := c.post("certificates", shared.Jmap{"type": "client", "certificate": b64, "name": name}, Sync)
	return err
}

func (c *Client) CertificateRemove(fingerprint string) error {
	_, err := c.delete(fmt.Sprintf("certificates/%s", fingerprint), nil, Sync)
	return err
}

func (c *Client) IsAlias(alias string) (bool, error) {
	_, err := c.get(fmt.Sprintf("images/aliases/%s", alias))
	if err != nil {
		if err == LXDErrors[http.StatusNotFound] {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (c *Client) GetAlias(alias string) string {
	resp, err := c.get(fmt.Sprintf("images/aliases/%s", alias))
	if err != nil {
		return ""
	}

	if resp.Type == Error {
		return ""
	}

	var result shared.ImageAlias
	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return ""
	}
	return result.Name
}

// Init creates a container from either a fingerprint or an alias; you must
// provide at least one.
func (c *Client) Init(name string, imgremote string, image string, profiles *[]string, ephem bool) (*Response, error) {
	var operation string
	var tmpremote *Client
	var err error

	serverStatus, err := c.ServerStatus()
	if err != nil {
		return nil, err
	}
	architectures := serverStatus.Environment.Architectures

	source := shared.Jmap{"type": "image"}

	if imgremote != "" {
		source["type"] = "image"
		source["mode"] = "pull"
		tmpremote, err = NewClient(&c.config, imgremote)
		if err != nil {
			return nil, err
		}

		fingerprint := tmpremote.GetAlias(image)
		if fingerprint == "" {
			fingerprint = image
		}

		imageinfo, err := tmpremote.GetImageInfo(fingerprint)
		if err != nil {
			return nil, err
		}

		if !shared.IntInSlice(imageinfo.Architecture, architectures) {
			return nil, fmt.Errorf(gettext.Gettext("The image architecture is incompatible with the target server"))
		}

		if imageinfo.Public == 0 {
			resp, err := tmpremote.post("images/"+fingerprint+"/secret", nil, Async)
			if err != nil {
				return nil, err
			}

			toScan := strings.Replace(resp.Operation, "/", " ", -1)
			version := ""
			count, err := fmt.Sscanf(toScan, " %s operations %s", &version, &operation)
			if err != nil || count != 2 {
				return nil, err
			}

			md := secretMd{}
			if err := json.Unmarshal(resp.Metadata, &md); err != nil {
				return nil, err
			}

			source["secret"] = md.Secret
		}

		source["server"] = tmpremote.BaseURL
		source["fingerprint"] = fingerprint
	} else {
		fingerprint := c.GetAlias(image)
		if fingerprint == "" {
			fingerprint = image
		}

		imageinfo, err := c.GetImageInfo(fingerprint)
		if err != nil {
			return nil, err
		}

		if !shared.IntInSlice(imageinfo.Architecture, architectures) {
			return nil, fmt.Errorf(gettext.Gettext("The image architecture is incompatible with the target server"))
		}
		source["fingerprint"] = fingerprint
	}

	body := shared.Jmap{"source": source}

	if name != "" {
		body["name"] = name
	}

	if profiles != nil {
		body["profiles"] = *profiles
	}

	if ephem {
		body["ephemeral"] = ephem
	}

	resp, err := c.post("containers", body, Async)

	if operation != "" {
		_, _ = tmpremote.delete("operations/"+operation, nil, Sync)
	}

	if err != nil {
		if LXDErrors[http.StatusNotFound] == err {
			return nil, fmt.Errorf("image doesn't exist")
		}
		return nil, err
	}

	return resp, nil
}

func (c *Client) LocalCopy(source string, name string, config map[string]string, profiles []string) (*Response, error) {
	body := shared.Jmap{
		"source": shared.Jmap{
			"type":   "copy",
			"source": source,
		},
		"name":     name,
		"config":   config,
		"profiles": profiles,
	}

	return c.post("containers", body, Async)
}

type execMd struct {
	FDs map[string]string `json:"fds"`
}

type secretMd struct {
	Secret string `json:"secret"`
}

func (c *Client) Exec(name string, cmd []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File) (int, error) {
	interactive := terminal.IsTerminal(int(stdin.Fd()))

	body := shared.Jmap{"command": cmd, "wait-for-websocket": true, "interactive": interactive, "environment": env}

	resp, err := c.post(fmt.Sprintf("containers/%s/exec", name), body, Async)
	if err != nil {
		return -1, err
	}

	md := execMd{}
	if err := json.Unmarshal(resp.Metadata, &md); err != nil {
		return -1, err
	}

	if interactive {
		if wsControl, ok := md.FDs["control"]; ok {
			go func() {
				control, err := c.websocket(resp.Operation, wsControl)
				if err != nil {
					return
				}

				for {
					width, height, err := terminal.GetSize(syscall.Stdout)
					if err != nil {
						continue
					}

					shared.Debugf("Window size is now: %dx%d\n", width, height)

					w, err := control.NextWriter(websocket.TextMessage)
					if err != nil {
						shared.Debugf("got error getting next writer %s", err)
						break
					}

					msg := shared.ContainerExecControl{}
					msg.Command = "window-resize"
					msg.Args = make(map[string]string)
					msg.Args["width"] = strconv.Itoa(width)
					msg.Args["height"] = strconv.Itoa(height)

					buf, err := json.Marshal(msg)
					if err != nil {
						shared.Debugf("failed to convert to json %s", err)
						break
					}
					_, err = w.Write(buf)

					w.Close()
					if err != nil {
						shared.Debugf("got err writing %s", err)
						break
					}

					ch := make(chan os.Signal)
					signal.Notify(ch, syscall.SIGWINCH)
					sig := <-ch

					shared.Debugf("Received '%s signal', updating window geometry.\n", sig)
				}

				closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
				control.WriteMessage(websocket.CloseMessage, closeMsg)
			}()
		}

		conn, err := c.websocket(resp.Operation, md.FDs["0"])
		if err != nil {
			return -1, err
		}
		shared.WebsocketSendStream(conn, stdin)
		<-shared.WebsocketRecvStream(stdout, conn)
	} else {
		sources := []*os.File{stdin, stdout, stderr}
		conns := make([]*websocket.Conn, 3)
		dones := make([]chan bool, 3)
		for i := 0; i < 3; i++ {
			conns[i], err = c.websocket(resp.Operation, md.FDs[strconv.Itoa(i)])
			if err != nil {
				return -1, err
			}

			if i == 0 {
				dones[i] = shared.WebsocketSendStream(conns[i], sources[i])
			} else {
				dones[i] = shared.WebsocketRecvStream(sources[i], conns[i])
			}
		}

		/*
		 * We'll get a read signal from each of stdout, stderr when they've
		 * both died. We need to wait for these in addition to the operation,
		 * because the server may indicate that the operation is done before we
		 * can actually read the last bits of data off these sockets and print
		 * it to the screen.
		 *
		 * We don't wait for stdin here, because if we're interactive, the user
		 * may not have closed it (e.g. if the command exits but the user
		 * didn't ^D).
		 */
		for i := 1; i < 3; i++ {
			<-dones[i]
		}

		// Once we're done, we explicitly close stdin, to signal the websockets
		// we're done.
		sources[0].Close()
	}

	// Now, get the operation's status too.
	op, err := c.WaitFor(resp.Operation)
	if err != nil {
		return -1, err
	}

	if op.StatusCode == shared.Failure {
		return -1, op.GetError()
	}

	if op.StatusCode != shared.Success {
		return -1, fmt.Errorf(gettext.Gettext("got bad op status %s"), op.Status)
	}

	opMd, err := op.MetadataAsMap()
	if err != nil {
		return -1, err
	}

	return opMd.GetInt("return")
}

func (c *Client) Action(name string, action shared.ContainerAction, timeout int, force bool) (*Response, error) {
	body := shared.Jmap{"action": action, "timeout": timeout, "force": force}
	return c.put(fmt.Sprintf("containers/%s/state", name), body, Async)
}

func (c *Client) Delete(name string) (*Response, error) {
	var url string
	s := strings.SplitN(name, "/", 2)
	if len(s) == 2 {
		url = fmt.Sprintf("containers/%s/snapshots/%s", s[0], s[1])
	} else {
		url = fmt.Sprintf("containers/%s", name)
	}

	return c.delete(url, nil, Async)
}

func (c *Client) ServerStatus() (*shared.ServerState, error) {
	ss := shared.ServerState{}

	resp, err := c.GetServerConfig()
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(resp.Metadata, &ss); err != nil {
		return nil, err
	}

	return &ss, nil
}

func (c *Client) ContainerStatus(name string, showLog bool) (*shared.ContainerState, error) {
	ct := shared.ContainerState{}
	query := url.Values{"log": []string{fmt.Sprintf("%v", showLog)}}

	resp, err := c.get(fmt.Sprintf("containers/%s", name) + "?" + query.Encode())
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(resp.Metadata, &ct); err != nil {
		return nil, err
	}

	return &ct, nil
}

func (c *Client) ProfileConfig(name string) (*shared.ProfileConfig, error) {
	ct := shared.ProfileConfig{}

	resp, err := c.get(fmt.Sprintf("profiles/%s", name))
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(resp.Metadata, &ct); err != nil {
		return nil, err
	}

	return &ct, nil
}

func (c *Client) PushFile(container string, p string, gid int, uid int, mode os.FileMode, buf io.ReadSeeker) error {
	query := url.Values{"path": []string{p}}
	uri := c.url(shared.APIVersion, "containers", container, "files") + "?" + query.Encode()

	req, err := http.NewRequest("POST", uri, buf)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", shared.UserAgent)

	req.Header.Set("X-LXD-mode", fmt.Sprintf("%04o", mode))
	req.Header.Set("X-LXD-uid", strconv.FormatUint(uint64(uid), 10))
	req.Header.Set("X-LXD-gid", strconv.FormatUint(uint64(gid), 10))

	raw, err := c.http.Do(req)
	if err != nil {
		return err
	}

	_, err = HoistResponse(raw, Sync)
	return err
}

func (c *Client) PullFile(container string, p string) (int, int, os.FileMode, io.ReadCloser, error) {
	uri := c.url(shared.APIVersion, "containers", container, "files")
	query := url.Values{"path": []string{p}}

	r, err := c.getRaw(uri + "?" + query.Encode())
	if err != nil {
		return 0, 0, 0, nil, err
	}

	uid, gid, mode := shared.ParseLXDFileHeaders(r.Header)

	return uid, gid, mode, r.Body, nil
}

func (c *Client) MigrateTo(container string) (*Response, error) {
	body := shared.Jmap{"migration": true}
	return c.post(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) MigrateFrom(name string, operation string, secrets map[string]string, config map[string]string, profiles []string, baseImage string) (*Response, error) {
	source := shared.Jmap{
		"type":       "migration",
		"mode":       "pull",
		"operation":  operation,
		"secrets":    secrets,
		"base-image": baseImage,
	}
	body := shared.Jmap{
		"source":   source,
		"name":     name,
		"config":   config,
		"profiles": profiles,
	}

	return c.post("containers", body, Async)
}

func (c *Client) Rename(name string, newName string) (*Response, error) {
	body := shared.Jmap{"name": newName}
	return c.post(fmt.Sprintf("containers/%s", name), body, Async)
}

/* Wait for an operation */
func (c *Client) WaitFor(waitURL string) (*shared.Operation, error) {
	if len(waitURL) < 1 {
		return nil, fmt.Errorf(gettext.Gettext("invalid wait url %s"), waitURL)
	}

	/* For convenience, waitURL is expected to be in the form of a
	 * Response.Operation string, i.e. it already has
	 * "/<version>/operations/" in it; we chop off the leading / and pass
	 * it to url directly.
	 */
	shared.Debugf(path.Join(waitURL[1:], "wait"))
	resp, err := c.baseGet(c.url(waitURL, "wait"))
	if err != nil {
		return nil, err
	}

	return resp.MetadataAsOperation()
}

func (c *Client) WaitForSuccess(waitURL string) error {
	op, err := c.WaitFor(waitURL)
	if err != nil {
		return err
	}

	if op.StatusCode == shared.Success {
		return nil
	}

	return op.GetError()
}

func (c *Client) RestoreSnapshot(container string, snapshotName string, stateful bool) (*Response, error) {
	body := shared.Jmap{"restore": snapshotName, "stateful": stateful}
	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) Snapshot(container string, snapshotName string, stateful bool) (*Response, error) {
	body := shared.Jmap{"name": snapshotName, "stateful": stateful}
	return c.post(fmt.Sprintf("containers/%s/snapshots", container), body, Async)
}

func (c *Client) ListSnapshots(container string) ([]string, error) {
	qUrl := fmt.Sprintf("containers/%s/snapshots?recursion=1", container)
	resp, err := c.get(qUrl)
	if err != nil {
		return nil, err
	}

	var result []shared.Jmap

	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	names := []string{}

	for _, snapjmap := range result {
		name, err := snapjmap.GetString("name")
		if err != nil {
			continue
		}
		names = append(names, name)
	}

	return names, nil
}

func (c *Client) GetServerConfigString() ([]string, error) {
	ss, err := c.ServerStatus()
	var resp []string
	if err != nil {
		return resp, err
	}

	if ss.Auth == "untrusted" {
		return resp, nil
	}

	if len(ss.Config) == 0 {
		resp = append(resp, "No config variables set.")
	}

	for k, v := range ss.Config {
		resp = append(resp, fmt.Sprintf("%s = %v", k, v))
	}

	return resp, nil
}

func (c *Client) SetServerConfig(key string, value string) (*Response, error) {
	body := shared.Jmap{"config": shared.Jmap{key: value}}
	return c.put("", body, Sync)
}

/*
 * return string array representing a container's full configuration
 */
func (c *Client) GetContainerConfig(container string) ([]string, error) {
	st, err := c.ContainerStatus(container, false)
	var resp []string
	if err != nil {
		return resp, err
	}

	profiles := strings.Join(st.Profiles, ",")
	pstr := fmt.Sprintf("Profiles: %s", profiles)

	resp = append(resp, pstr)
	for k, v := range st.Config {
		str := fmt.Sprintf("%s = %s", k, v)
		resp = append(resp, str)
	}

	return resp, nil
}

func (c *Client) SetContainerConfig(container, key, value string) (*Response, error) {
	st, err := c.ContainerStatus(container, false)
	if err != nil {
		return nil, err
	}

	if value == "" {
		delete(st.Config, key)
	} else {
		st.Config[key] = value
	}

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": container, "devices": st.Devices}
	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) UpdateContainerConfig(container string, st shared.BriefContainerState) error {
	body := shared.Jmap{"name": container,
		"profiles":  st.Profiles,
		"config":    st.Config,
		"devices":   st.Devices,
		"ephemeral": st.Ephemeral}
	_, err := c.put(fmt.Sprintf("containers/%s", container), body, Async)
	return err
}

func (c *Client) ProfileCreate(p string) error {
	body := shared.Jmap{"name": p}

	_, err := c.post("profiles", body, Sync)
	return err
}

func (c *Client) ProfileDelete(p string) error {
	_, err := c.delete(fmt.Sprintf("profiles/%s", p), nil, Sync)
	return err
}

func (c *Client) GetProfileConfig(profile string) (map[string]string, error) {
	st, err := c.ProfileConfig(profile)
	if err != nil {
		return nil, err
	}

	return st.Config, nil
}

func (c *Client) SetProfileConfigItem(profile, key, value string) error {
	st, err := c.ProfileConfig(profile)
	if err != nil {
		shared.Debugf("Error getting profile %s to update\n", profile)
		return err
	}

	if value == "" {
		delete(st.Config, key)
	} else {
		st.Config[key] = value
	}

	body := shared.Jmap{"name": profile, "config": st.Config, "devices": st.Devices}
	_, err = c.put(fmt.Sprintf("profiles/%s", profile), body, Sync)
	return err
}

func (c *Client) PutProfile(name string, profile shared.ProfileConfig) error {
	if profile.Name != name {
		return fmt.Errorf(gettext.Gettext("Cannot change profile name"))
	}
	body := shared.Jmap{"name": name, "config": profile.Config, "devices": profile.Devices}
	_, err := c.put(fmt.Sprintf("profiles/%s", name), body, Sync)
	return err
}

func (c *Client) ListProfiles() ([]string, error) {
	resp, err := c.get("profiles")
	if err != nil {
		return nil, err
	}

	var result []string

	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	names := []string{}

	for _, url := range result {
		toScan := strings.Replace(url, "/", " ", -1)
		version := ""
		name := ""
		count, err := fmt.Sscanf(toScan, " %s profiles %s", &version, &name)
		if err != nil {
			return nil, err
		}

		if count != 2 {
			return nil, fmt.Errorf(gettext.Gettext("bad profile url %s"), url)
		}

		if version != shared.APIVersion {
			return nil, fmt.Errorf(gettext.Gettext("bad version in profile url"))
		}

		names = append(names, name)
	}

	return names, nil
}

func (c *Client) ApplyProfile(container, profile string) (*Response, error) {
	st, err := c.ContainerStatus(container, false)
	if err != nil {
		return nil, err
	}
	profiles := strings.Split(profile, ",")
	body := shared.Jmap{"config": st.Config, "profiles": profiles, "name": st.Name, "devices": st.Devices}

	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) ContainerDeviceDelete(container, devname string) (*Response, error) {
	st, err := c.ContainerStatus(container, false)
	if err != nil {
		return nil, err
	}

	delete(st.Devices, devname)

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": st.Name, "devices": st.Devices}
	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) ContainerDeviceAdd(container, devname, devtype string, props []string) (*Response, error) {
	st, err := c.ContainerStatus(container, false)
	if err != nil {
		return nil, err
	}

	newdev := shared.Device{}
	for _, p := range props {
		results := strings.SplitN(p, "=", 2)
		if len(results) != 2 {
			return nil, fmt.Errorf(gettext.Gettext("no value found in %q\n"), p)
		}
		k := results[0]
		v := results[1]
		newdev[k] = v
	}
	if st.Devices != nil && st.Devices.ContainsName(devname) {
		return nil, fmt.Errorf(gettext.Gettext("device already exists\n"))
	}
	newdev["type"] = devtype
	if st.Devices == nil {
		st.Devices = shared.Devices{}
	}
	st.Devices[devname] = newdev

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": st.Name, "devices": st.Devices}
	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) ContainerListDevices(container string) ([]string, error) {
	st, err := c.ContainerStatus(container, false)
	if err != nil {
		return nil, err
	}
	devs := []string{}
	for n, d := range st.Devices {
		devs = append(devs, fmt.Sprintf("%s: %s", n, d["type"]))
	}
	return devs, nil
}

func (c *Client) ProfileDeviceDelete(profile, devname string) (*Response, error) {
	st, err := c.ProfileConfig(profile)
	if err != nil {
		return nil, err
	}

	for n, _ := range st.Devices {
		if n == devname {
			delete(st.Devices, n)
		}
	}

	body := shared.Jmap{"config": st.Config, "name": st.Name, "devices": st.Devices}
	return c.put(fmt.Sprintf("profiles/%s", profile), body, Sync)
}

func (c *Client) ProfileDeviceAdd(profile, devname, devtype string, props []string) (*Response, error) {
	st, err := c.ProfileConfig(profile)
	if err != nil {
		return nil, err
	}

	newdev := shared.Device{}
	for _, p := range props {
		results := strings.SplitN(p, "=", 2)
		if len(results) != 2 {
			return nil, fmt.Errorf(gettext.Gettext("no value found in %q\n"), p)
		}
		k := results[0]
		v := results[1]
		newdev[k] = v
	}
	if st.Devices != nil && st.Devices.ContainsName(devname) {
		return nil, fmt.Errorf(gettext.Gettext("device already exists\n"))
	}
	newdev["type"] = devtype
	if st.Devices == nil {
		st.Devices = shared.Devices{}
	}
	st.Devices[devname] = newdev

	body := shared.Jmap{"config": st.Config, "name": st.Name, "devices": st.Devices}
	return c.put(fmt.Sprintf("profiles/%s", profile), body, Sync)
}

func (c *Client) ProfileListDevices(profile string) ([]string, error) {
	st, err := c.ProfileConfig(profile)
	if err != nil {
		return nil, err
	}
	devs := []string{}
	for n, d := range st.Devices {
		devs = append(devs, fmt.Sprintf("%s: %s", n, d["type"]))
	}
	return devs, nil

}

// WebsocketDial attempts to dial a websocket to a LXD instance, parsing
// LXD-style errors and returning them as go errors.
func WebsocketDial(dialer websocket.Dialer, url string) (*websocket.Conn, error) {
	conn, raw, err := dialer.Dial(url, http.Header{})
	if err != nil {
		_, err2 := HoistResponse(raw, Error)
		if err2 != nil {
			/* The response isn't one we understand, so return
			 * whatever the original error was. */
			return nil, err
		}

		return nil, err
	}
	return conn, err
}

func (c *Client) ProfileCopy(name, newname string, dest *Client) error {
	st, err := c.ProfileConfig(name)
	if err != nil {
		return err
	}

	body := shared.Jmap{"config": st.Config, "name": newname, "devices": st.Devices}
	_, err = dest.post("profiles", body, Sync)
	return err
}

func (c *Client) ImageFromContainer(cname string, public bool, aliases []string, properties map[string]string) (string, error) {
	source := shared.Jmap{"type": "container", "name": cname}
	if shared.IsSnapshot(cname) {
		source["type"] = "snapshot"
	}
	body := shared.Jmap{"public": public, "source": source, "properties": properties}

	resp, err := c.post("images", body, Sync)
	if err != nil {
		return "", err
	}

	jmap, err := resp.MetadataAsMap()
	if err != nil {
		return "", err
	}

	fingerprint, err := jmap.GetString("fingerprint")
	if err != nil {
		return "", err
	}

	/* add new aliases */
	for _, alias := range aliases {
		c.DeleteAlias(alias)
		err = c.PostAlias(alias, alias, fingerprint)
		if err != nil {
			fmt.Printf(gettext.Gettext("Error adding alias %s\n"), alias)
		}
	}

	return fingerprint, nil
}
