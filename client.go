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
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
)

// Client can talk to a LXD daemon.
type Client struct {
	BaseURL   string
	BaseWSURL string
	Config    Config
	Http      http.Client
	Name      string
	Remote    *RemoteConfig
	Transport string

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
	Operation string `json:"operation"`

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
		return nil, fmt.Errorf(i18n.G("no response!"))
	}
	defer r.Body.Close()
	ret := Response{}

	s, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	shared.Debugf("Raw response: %s", string(s))

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
		return nil, fmt.Errorf(i18n.G("got bad response type, expected %s got %s"), rtype, resp.Type)
	}

	return resp, nil
}

func readMyCert(configDir string) (string, string, error) {
	certf := path.Join(configDir, "client.crt")
	keyf := path.Join(configDir, "client.key")

	err := shared.FindOrGenCert(certf, keyf)

	return certf, keyf, err
}

/*
 * load the server cert from disk
 */
func (c *Client) loadServerCert() {
	cert, err := shared.ReadCert(c.Config.ServerCertPath(c.Name))
	if err != nil {
		shared.Debugf("Error reading the server certificate for %s: %v", c.Name, err)
		return
	}

	c.scert = cert
}

// NewClient returns a new LXD client.
func NewClient(config *Config, remote string) (*Client, error) {
	c := Client{
		Config: *config,
		Http:   http.Client{},
	}

	c.Name = remote

	if remote == "" {
		return nil, fmt.Errorf(i18n.G("A remote name must be provided."))
	}

	if r, ok := config.Remotes[remote]; ok {
		if r.Addr[0:5] == "unix:" {
			if r.Addr == "unix://" {
				r.Addr = fmt.Sprintf("unix:%s", shared.VarPath("unix.socket"))
			}

			c.BaseURL = "http://unix.socket"
			c.BaseWSURL = "ws://unix.socket"
			c.Transport = "unix"
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
			c.Http.Transport = &http.Transport{Dial: uDial}
			c.websocketDialer.NetDial = uDial
			c.Remote = &r
		} else {
			certf, keyf, err := readMyCert(config.ConfigDir)
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
			c.Transport = "https"
			c.Http.Transport = tr
			c.loadServerCert()
			c.Remote = &r
		}
	} else {
		return nil, fmt.Errorf(i18n.G("unknown remote name: %q"), remote)
	}

	return &c, nil
}

func (c *Client) Addresses() ([]string, error) {
	addresses := make([]string, 0)

	if c.Transport == "unix" {
		serverStatus, err := c.ServerStatus()
		if err != nil {
			return nil, err
		}
		addresses = serverStatus.Environment.Addresses
	} else if c.Transport == "https" {
		addresses = append(addresses, c.BaseURL[8:])
	} else {
		return nil, fmt.Errorf(i18n.G("unknown transport type: %s"), c.Transport)
	}

	if len(addresses) == 0 {
		return nil, fmt.Errorf(i18n.G("The source remote isn't available over the network"))
	}

	return addresses, nil
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

	resp, err := c.Http.Do(req)
	if err != nil {
		return nil, err
	}

	if c.scert != nil && resp.TLS != nil {
		if !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, c.scert.Raw) {
			pUrl, _ := url.Parse(getUrl)
			return nil, fmt.Errorf(i18n.G("Server certificate for host %s has changed. Add correct certificate or remove certificate in %s"), pUrl.Host, c.Config.ConfigPath("servercerts"))
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

	shared.Debugf("Putting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("PUT", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Http.Do(req)
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

	shared.Debugf("Posting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("POST", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Http.Do(req)
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

	raw, err := c.Http.Do(req)
	if err != nil {
		return nil, err
	}

	// because it is raw data, we need to check for http status
	if raw.StatusCode != 200 {
		resp, err := HoistResponse(raw, Sync)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf(i18n.G("expected error, got %s"), resp)
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

	shared.Debugf("Deleting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("DELETE", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", shared.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Http.Do(req)
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

func (c *Client) GetServerConfig() (*Response, error) {
	return c.baseGet(c.url(shared.APIVersion))
}

func (c *Client) Finger() error {
	shared.Debugf("Fingering the daemon")
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
		return fmt.Errorf(i18n.G("api version mismatch: mine: %q, daemon: %q"), shared.APICompat, serverAPICompat)
	}
	shared.Debugf("Pong received")
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

func (c *Client) IsPublic() bool {
	resp, err := c.GetServerConfig()
	if err != nil {
		return false
	}

	shared.Debugf("%s", resp)

	jmap, err := resp.MetadataAsMap()
	if err != nil {
		return false
	}

	public, err := jmap.GetBool("public")
	if err != nil {
		return false
	}

	return public
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

func (c *Client) CopyImage(image string, dest *Client, copy_aliases bool, aliases []string, public bool, progressHandler func(progress string)) error {
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

	// FIXME: InterfaceToBool is there for backward compatibility
	if !shared.InterfaceToBool(info.Public) {
		var secret string

		resp, err := c.post("images/"+fingerprint+"/secret", nil, Async)
		if err != nil {
			return err
		}

		op, err := resp.MetadataAsOperation()
		if err == nil && op.Metadata != nil {
			secret, err = op.Metadata.GetString("secret")
			if err != nil {
				return err
			}
		} else {
			// FIXME: This is a backward compatibility codepath
			md := secretMd{}
			if err := json.Unmarshal(resp.Metadata, &md); err != nil {
				return err
			}

			secret = md.Secret
		}

		source["secret"] = secret
	}

	addresses, err := c.Addresses()
	if err != nil {
		return err
	}

	operation := ""
	handler := func(msg interface{}) {
		if msg == nil {
			return
		}

		event := msg.(map[string]interface{})
		if event["type"].(string) != "operation" {
			return
		}

		if event["metadata"] == nil {
			return
		}

		md := event["metadata"].(map[string]interface{})
		if !strings.HasSuffix(operation, md["id"].(string)) {
			return
		}

		if md["metadata"] == nil {
			return
		}

		opMd := md["metadata"].(map[string]interface{})
		_, ok := opMd["download_progress"]
		if ok {
			progressHandler(opMd["download_progress"].(string))
		}
	}

	if progressHandler != nil {
		go dest.Monitor([]string{"operation"}, handler)
	}

	for _, addr := range addresses {
		sourceUrl := "https://" + addr

		source["server"] = sourceUrl
		body := shared.Jmap{"public": public, "source": source}

		resp, err := dest.post("images", body, Async)
		if err != nil {
			continue
		}

		operation = resp.Operation

		err = dest.WaitForSuccess(resp.Operation)
		if err != nil {
			return err
		}

		break
	}

	if err != nil {
		return err
	}

	/* copy aliases from source image */
	if copy_aliases {
		for _, alias := range info.Aliases {
			dest.DeleteAlias(alias.Name)
			err = dest.PostAlias(alias.Name, alias.Description, info.Fingerprint)
			if err != nil {
				fmt.Printf(i18n.G("Error adding alias %s")+"\n", alias.Name)
			}
		}
	}

	/* add new aliases */
	for _, alias := range aliases {
		dest.DeleteAlias(alias)
		err = dest.PostAlias(alias, alias, info.Fingerprint)
		if err != nil {
			fmt.Printf(i18n.G("Error adding alias %s")+"\n", alias)
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
			return nil, "", fmt.Errorf(i18n.G("Split images can only be written to a directory."))
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

func (c *Client) PostImageURL(imageFile string, public bool, aliases []string) (string, error) {
	source := shared.Jmap{
		"type": "url",
		"mode": "pull",
		"url":  imageFile}
	body := shared.Jmap{"public": public, "source": source}

	resp, err := c.post("images", body, Async)
	if err != nil {
		return "", err
	}

	op, err := c.WaitFor(resp.Operation)
	if err != nil {
		return "", err
	}

	if op.Metadata == nil {
		return "", fmt.Errorf(i18n.G("Missing operation metadata"))
	}

	fingerprint, err := op.Metadata.GetString("fingerprint")
	if err != nil {
		return "", err
	}

	/* add new aliases */
	for _, alias := range aliases {
		c.DeleteAlias(alias)
		err = c.PostAlias(alias, alias, fingerprint)
		if err != nil {
			fmt.Printf(i18n.G("Error adding alias %s")+"\n", alias)
		}
	}

	return fingerprint, nil
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
				return "", fmt.Errorf(i18n.G("Bad image property: %s"), value)
			}

		}

		req.Header.Set("X-LXD-properties", imgProps.Encode())
	}

	raw, err := c.Http.Do(req)
	if err != nil {
		return "", err
	}

	resp, err := HoistResponse(raw, Async)
	if err != nil {
		return "", err
	}

	jmap, err := c.AsyncWaitMeta(resp)
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
			fmt.Printf(i18n.G("Error adding alias %s")+"\n", alias)
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

func (c *Client) PutImageInfo(name string, p shared.BriefImageInfo) error {
	body := shared.Jmap{}
	body["public"] = p.Public
	body["properties"] = p.Properties

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
		if err := c.Finger(); err != nil {
			return err
		}

		if !c.scertDigestSet {
			return fmt.Errorf(i18n.G("No certificate on this connection"))
		}
	}

	if c.scert != nil {
		return nil
	}

	_, err := c.scertWire.Verify(x509.VerifyOptions{
		DNSName:       name,
		Intermediates: c.scertIntermediates,
	})
	if err == nil {
		// Server trusted by system certificate
		return nil
	}

	if acceptCert == false {
		fmt.Printf(i18n.G("Certificate fingerprint: %x")+"\n", c.scertDigest)
		fmt.Printf(i18n.G("ok (y/n)?") + " ")
		line, err := shared.ReadStdin()
		if err != nil {
			return err
		}

		if len(line) < 1 || line[0] != 'y' && line[0] != 'Y' {
			return fmt.Errorf(i18n.G("Server certificate NACKed by user"))
		}
	}

	// User acked the cert, now add it to our store
	dnam := c.Config.ConfigPath("servercerts")
	err = os.MkdirAll(dnam, 0750)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not create server cert dir"))
	}
	certf := fmt.Sprintf("%s/%s.crt", dnam, c.Name)
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
func (c *Client) Init(name string, imgremote string, image string, profiles *[]string, config map[string]string, ephem bool) (*Response, error) {
	var tmpremote *Client
	var err error

	serverStatus, err := c.ServerStatus()
	if err != nil {
		return nil, err
	}
	architectures := serverStatus.Environment.Architectures

	source := shared.Jmap{"type": "image"}

	if image == "" {
		return nil, fmt.Errorf(i18n.G("You must provide an image hash or alias name."))
	}

	if imgremote != c.Name {
		source["type"] = "image"
		source["mode"] = "pull"
		tmpremote, err = NewClient(&c.Config, imgremote)
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

		if len(architectures) != 0 && !shared.IntInSlice(imageinfo.Architecture, architectures) {
			return nil, fmt.Errorf(i18n.G("The image architecture is incompatible with the target server"))
		}

		// FIXME: InterfaceToBool is there for backward compatibility
		if !shared.InterfaceToBool(imageinfo.Public) {
			var secret string

			resp, err := tmpremote.post("images/"+fingerprint+"/secret", nil, Async)
			if err != nil {
				return nil, err
			}

			op, err := resp.MetadataAsOperation()
			if err == nil && op.Metadata != nil {
				secret, err = op.Metadata.GetString("secret")
				if err != nil {
					return nil, err
				}
			} else {
				// FIXME: This is a backward compatibility codepath
				md := secretMd{}
				if err := json.Unmarshal(resp.Metadata, &md); err != nil {
					return nil, err
				}

				secret = md.Secret
			}

			source["secret"] = secret
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
			return nil, fmt.Errorf(i18n.G("can't get info for image '%s': %s"), image, err)
		}

		if len(architectures) != 0 && !shared.IntInSlice(imageinfo.Architecture, architectures) {
			return nil, fmt.Errorf(i18n.G("The image architecture is incompatible with the target server"))
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

	if config != nil {
		body["config"] = config
	}

	if ephem {
		body["ephemeral"] = ephem
	}

	var resp *Response

	if imgremote != c.Name {
		var addresses []string
		addresses, err = tmpremote.Addresses()
		if err != nil {
			return nil, err
		}

		for _, addr := range addresses {
			body["source"].(shared.Jmap)["server"] = "https://" + addr

			resp, err = c.post("containers", body, Async)
			if err != nil {
				continue
			}

			break
		}
	} else {
		resp, err = c.post("containers", body, Async)
	}

	if err != nil {
		if LXDErrors[http.StatusNotFound] == err {
			return nil, fmt.Errorf("image doesn't exist")
		}
		return nil, err
	}

	return resp, nil
}

func (c *Client) LocalCopy(source string, name string, config map[string]string, profiles []string, ephemeral bool) (*Response, error) {
	body := shared.Jmap{
		"source": shared.Jmap{
			"type":   "copy",
			"source": source,
		},
		"name":      name,
		"config":    config,
		"profiles":  profiles,
		"ephemeral": ephemeral,
	}

	return c.post("containers", body, Async)
}

type execMd struct {
	FDs map[string]string `json:"fds"`
}

type secretMd struct {
	Secret string `json:"secret"`
}

func (c *Client) Monitor(types []string, handler func(interface{})) error {
	url := c.BaseWSURL + path.Join("/", "1.0", "events")
	if len(types) != 0 {
		url += "?type=" + strings.Join(types, ",")
	}

	conn, err := WebsocketDial(c.websocketDialer, url)
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		message := make(map[string]interface{})

		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		err = json.Unmarshal(data, &message)
		if err != nil {
			return err
		}

		handler(message)
	}
}

// Exec runs a command inside the LXD container. For "interactive" use such as
// `lxc exec ...`, one should pass a controlHandler that talks over the control
// socket and handles things like SIGWINCH. If running non-interactive, passing
// a nil controlHandler will cause Exec to return when all of the command
// output is sent to the output buffers.
func (c *Client) Exec(name string, cmd []string, env map[string]string,
	stdin io.ReadCloser, stdout io.WriteCloser,
	stderr io.WriteCloser, controlHandler func(*Client, *websocket.Conn)) (int, error) {

	body := shared.Jmap{
		"command":            cmd,
		"wait-for-websocket": true,
		"interactive":        controlHandler != nil,
		"environment":        env,
	}

	resp, err := c.post(fmt.Sprintf("containers/%s/exec", name), body, Async)
	if err != nil {
		return -1, err
	}

	var fds shared.Jmap

	op, err := resp.MetadataAsOperation()
	if err == nil && op.Metadata != nil {
		fds, err = op.Metadata.GetMap("fds")
		if err != nil {
			return -1, err
		}
	} else {
		// FIXME: This is a backward compatibility codepath
		md := execMd{}
		if err := json.Unmarshal(resp.Metadata, &md); err != nil {
			return -1, err
		}

		fds, err = shared.ParseMetadata(md.FDs)
		if err != nil {
			return -1, err
		}
	}

	if controlHandler != nil {
		var control *websocket.Conn
		if wsControl, ok := fds["control"]; ok {
			control, err = c.websocket(resp.Operation, wsControl.(string))
			if err != nil {
				return -1, err
			}
			defer control.Close()

			go controlHandler(c, control)
		}

		conn, err := c.websocket(resp.Operation, fds["0"].(string))
		if err != nil {
			return -1, err
		}

		shared.WebsocketSendStream(conn, stdin)
		<-shared.WebsocketRecvStream(stdout, conn)
		conn.Close()

	} else {
		conns := make([]*websocket.Conn, 3)
		dones := make([]chan bool, 3)

		conns[0], err = c.websocket(resp.Operation, fds[strconv.Itoa(0)].(string))
		if err != nil {
			return -1, err
		}
		defer conns[0].Close()

		dones[0] = shared.WebsocketSendStream(conns[0], stdin)

		outputs := []io.WriteCloser{stdout, stderr}
		for i := 1; i < 3; i++ {
			conns[i], err = c.websocket(resp.Operation, fds[strconv.Itoa(i)].(string))
			if err != nil {
				return -1, err
			}
			defer conns[i].Close()

			dones[i] = shared.WebsocketRecvStream(outputs[i-1], conns[i])
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
		stdin.Close()
	}

	// Now, get the operation's status too.
	op, err = c.WaitFor(resp.Operation)
	if err != nil {
		return -1, err
	}

	if op.StatusCode == shared.Failure {
		return -1, fmt.Errorf(op.Err)
	}

	if op.StatusCode != shared.Success {
		return -1, fmt.Errorf(i18n.G("got bad op status %s"), op.Status)
	}

	if op.Metadata == nil {
		return -1, fmt.Errorf(i18n.G("no metadata received"))
	}

	return op.Metadata.GetInt("return")
}

func (c *Client) Action(name string, action shared.ContainerAction, timeout int, force bool) (*Response, error) {
	if action == "start" {
		current, err := c.ContainerStatus(name)
		if err == nil && current.Status.StatusCode == shared.Frozen {
			action = "unfreeze"
		}
	}

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

func (c *Client) ContainerStatus(name string) (*shared.ContainerState, error) {
	ct := shared.ContainerState{}

	resp, err := c.get(fmt.Sprintf("containers/%s", name))
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(resp.Metadata, &ct); err != nil {
		return nil, err
	}

	return &ct, nil
}

func (c *Client) GetLog(container string, log string) (io.Reader, error) {
	uri := c.url(shared.APIVersion, "containers", container, "logs", log)
	resp, err := c.getRaw(uri)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
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

	raw, err := c.Http.Do(req)
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

func (c *Client) GetMigrationSourceWS(container string) (*Response, error) {
	body := shared.Jmap{"migration": true}
	url := fmt.Sprintf("containers/%s", container)
	if shared.IsSnapshot(container) {
		pieces := strings.SplitN(container, shared.SnapshotDelimiter, 2)
		if len(pieces) != 2 {
			return nil, fmt.Errorf("invalid snapshot name %s", container)
		}

		url = fmt.Sprintf("containers/%s/snapshots/%s", pieces[0], pieces[1])
	}

	return c.post(url, body, Async)
}

func (c *Client) MigrateFrom(name string, operation string, secrets map[string]string, architecture int, config map[string]string, devices shared.Devices, profiles []string, baseImage string, ephemeral bool) (*Response, error) {
	source := shared.Jmap{
		"type":       "migration",
		"mode":       "pull",
		"operation":  operation,
		"secrets":    secrets,
		"base-image": baseImage,
	}
	body := shared.Jmap{
		"architecture": architecture,
		"config":       config,
		"devices":      devices,
		"ephemeral":    ephemeral,
		"name":         name,
		"profiles":     profiles,
		"source":       source,
	}

	return c.post("containers", body, Async)
}

func (c *Client) Rename(name string, newName string) (*Response, error) {
	oldNameParts := strings.SplitN(name, "/", 2)
	newNameParts := strings.SplitN(newName, "/", 2)
	if len(oldNameParts) != len(newNameParts) {
		return nil, fmt.Errorf("Attempting to rename container to snapshot or vice versa.")
	}
	if len(oldNameParts) == 1 {
		body := shared.Jmap{"name": newName}
		return c.post(fmt.Sprintf("containers/%s", name), body, Async)
	}
	if oldNameParts[0] != newNameParts[0] {
		return nil, fmt.Errorf("Attempting to rename snapshot of one container into a snapshot of another container.")
	}
	body := shared.Jmap{"name": newNameParts[1]}
	return c.post(fmt.Sprintf("containers/%s/snapshots/%s", oldNameParts[0], oldNameParts[1]), body, Async)
}

/* Wait for an operation */
func (c *Client) WaitFor(waitURL string) (*shared.Operation, error) {
	if len(waitURL) < 1 {
		return nil, fmt.Errorf(i18n.G("invalid wait url %s"), waitURL)
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

	return fmt.Errorf(op.Err)
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
	var resp []string

	ss, err := c.ServerStatus()
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
	ss, err := c.ServerStatus()
	if err != nil {
		return nil, err
	}

	ss.Config[key] = value
	body := shared.Jmap{
		"api_compat":  ss.APICompat,
		"auth":        ss.Auth,
		"environment": ss.Environment,
		"config":      ss.Config,
		"public":      ss.Public}

	return c.put("", body, Sync)
}

func (c *Client) UpdateServerConfig(ss shared.BriefServerState) (*Response, error) {
	body := shared.Jmap{"config": ss.Config}

	return c.put("", body, Sync)
}

/*
 * return string array representing a container's full configuration
 */
func (c *Client) GetContainerConfig(container string) ([]string, error) {
	var resp []string

	st, err := c.ContainerStatus(container)
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

func (c *Client) SetContainerConfig(container, key, value string) error {
	st, err := c.ContainerStatus(container)
	if err != nil {
		return err
	}

	if value == "" {
		delete(st.Config, key)
	} else {
		st.Config[key] = value
	}

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": container, "devices": st.Devices}
	/*
	 * Although container config is an async operation (we PUT to restore a
	 * snapshot), we expect config to be a sync operation, so let's just
	 * handle it here.
	 */
	resp, err := c.put(fmt.Sprintf("containers/%s", container), body, Async)
	if err != nil {
		return err
	}

	return c.WaitForSuccess(resp.Operation)
}

func (c *Client) UpdateContainerConfig(container string, st shared.BriefContainerState) error {
	body := shared.Jmap{"name": container,
		"profiles":  st.Profiles,
		"config":    st.Config,
		"devices":   st.Devices,
		"ephemeral": st.Ephemeral}
	resp, err := c.put(fmt.Sprintf("containers/%s", container), body, Async)
	if err != nil {
		return err
	}

	return c.WaitForSuccess(resp.Operation)
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
		shared.Debugf("Error getting profile %s to update", profile)
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
		return fmt.Errorf(i18n.G("Cannot change profile name"))
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
			return nil, fmt.Errorf(i18n.G("bad profile url %s"), url)
		}

		if version != shared.APIVersion {
			return nil, fmt.Errorf(i18n.G("bad version in profile url"))
		}

		names = append(names, name)
	}

	return names, nil
}

func (c *Client) ApplyProfile(container, profile string) (*Response, error) {
	st, err := c.ContainerStatus(container)
	if err != nil {
		return nil, err
	}
	profiles := strings.Split(profile, ",")
	body := shared.Jmap{"config": st.Config, "profiles": profiles, "name": st.Name, "devices": st.Devices}

	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) ContainerDeviceDelete(container, devname string) (*Response, error) {
	st, err := c.ContainerStatus(container)
	if err != nil {
		return nil, err
	}

	delete(st.Devices, devname)

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": st.Name, "devices": st.Devices}
	return c.put(fmt.Sprintf("containers/%s", container), body, Async)
}

func (c *Client) ContainerDeviceAdd(container, devname, devtype string, props []string) (*Response, error) {
	st, err := c.ContainerStatus(container)
	if err != nil {
		return nil, err
	}

	newdev := shared.Device{}
	for _, p := range props {
		results := strings.SplitN(p, "=", 2)
		if len(results) != 2 {
			return nil, fmt.Errorf(i18n.G("no value found in %q"), p)
		}
		k := results[0]
		v := results[1]
		newdev[k] = v
	}
	if st.Devices != nil && st.Devices.ContainsName(devname) {
		return nil, fmt.Errorf(i18n.G("device already exists"))
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
	st, err := c.ContainerStatus(container)
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
			return nil, fmt.Errorf(i18n.G("no value found in %q"), p)
		}
		k := results[0]
		v := results[1]
		newdev[k] = v
	}
	if st.Devices != nil && st.Devices.ContainsName(devname) {
		return nil, fmt.Errorf(i18n.G("device already exists"))
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

func (c *Client) AsyncWaitMeta(resp *Response) (*shared.Jmap, error) {
	op, err := c.WaitFor(resp.Operation)
	if err != nil {
		return nil, err
	}

	if op.StatusCode == shared.Failure {
		return nil, fmt.Errorf(op.Err)
	}

	if op.StatusCode != shared.Success {
		return nil, fmt.Errorf(i18n.G("got bad op status %s"), op.Status)
	}

	return op.Metadata, nil
}

func (c *Client) ImageFromContainer(cname string, public bool, aliases []string, properties map[string]string) (string, error) {
	source := shared.Jmap{"type": "container", "name": cname}
	if shared.IsSnapshot(cname) {
		source["type"] = "snapshot"
	}
	body := shared.Jmap{"public": public, "source": source, "properties": properties}

	resp, err := c.post("images", body, Async)
	if err != nil {
		return "", err
	}

	jmap, err := c.AsyncWaitMeta(resp)
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
			fmt.Printf(i18n.G("Error adding alias %s")+"\n", alias)
		}
	}

	return fingerprint, nil
}
