package lxd

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/gosexy/gettext"
)

// Client can talk to a lxd daemon.
type Client struct {
	config          Config
	Remote          *RemoteConfig
	name            string
	http            http.Client
	baseURL         string
	baseWSURL       string
	certf           string
	keyf            string
	cert            tls.Certificate
	websocketDialer websocket.Dialer

	scert *x509.Certificate // the cert stored on disk

	scertWire      *x509.Certificate // the cert from the tls connection
	scertDigest    [sha256.Size]byte // fingerprint of server cert from connection
	scertDigestSet bool              // whether we've stored the fingerprint
}

type ResponseType string

const (
	Sync  ResponseType = "sync"
	Async ResponseType = "async"
	Error ResponseType = "error"
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

func (r *Response) MetadataAsMap() (*Jmap, error) {
	ret := Jmap{}
	if err := json.Unmarshal(r.Metadata, &ret); err != nil {
		return nil, err
	}
	return &ret, nil
}

func (r *Response) MetadataAsOperation() (*Operation, error) {
	op := Operation{}
	if err := json.Unmarshal(r.Metadata, &op); err != nil {
		return nil, err
	}

	return &op, nil
}

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
	Debugf("raw response: %s", string(s))

	if err := json.Unmarshal(s, &ret); err != nil {
		return nil, err
	}

	return &ret, nil
}

func ParseError(r *Response) error {
	if r.Type == Error {
		return fmt.Errorf(r.Error)
	}

	return nil
}

func readMyCert() (string, string, error) {
	certf := configPath("client.crt")
	keyf := configPath("client.key")

	err := FindOrGenCert(certf, keyf)

	return certf, keyf, err
}

/*
 * load the server cert from disk
 */
func (c *Client) loadServerCert() {
	cert, err := ReadCert(ServerCertPath(c.name))
	if err != nil {
		Debugf("Error reading the server certificate for %s: %v\n", c.name, err)
		return
	}

	c.scert = cert
}

// NewClient returns a new lxd client.
func NewClient(config *Config, remote string) (*Client, error) {
	certf, keyf, err := readMyCert()
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certf, keyf)
	if err != nil {
		return nil, err
	}

	tlsconfig := &tls.Config{InsecureSkipVerify: true,
		ClientAuth:   tls.RequireAnyClientCert,
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12}
	tlsconfig.BuildNameToCertificate()

	tr := &http.Transport{
		TLSClientConfig: tlsconfig,
	}
	c := Client{
		config: *config,
		http: http.Client{
			Transport: tr,
			// Added on Go 1.3. Wait until it's more popular.
			//Timeout: 10 * time.Second,
		},
	}

	c.certf = certf
	c.keyf = keyf
	c.cert = cert
	c.websocketDialer = websocket.Dialer{
		TLSClientConfig: tlsconfig,
	}

	c.name = remote

	// TODO: Here, we don't support configurable local remotes, we only
	// support the default local lxd at /var/lib/lxd/unix.socket.
	if remote == "" {
		c.baseURL = "http://unix.socket"
		c.baseWSURL = "ws://unix.socket"
		c.http.Transport = &unixTransport
		c.websocketDialer.NetDial = unixDial
	} else if len(remote) > 6 && remote[0:5] == "unix:" {
		/*
		 * TODO: I suspect this doesn't work, since unixTransport
		 * hardcodes VarPath("unix.socket"); we should figure out
		 * whether or not unix: is really in the spec, and pass this
		 * down accordingly if it is.
		 */
		c.baseURL = "http://unix.socket"
		c.baseWSURL = "ws://unix.socket"
		c.http.Transport = &unixTransport
		c.websocketDialer.NetDial = unixDial
	} else if r, ok := config.Remotes[remote]; ok {
		c.baseURL = "https://" + r.Addr
		c.baseWSURL = "wss://" + r.Addr
		c.Remote = &r
		c.loadServerCert()
	} else {
		return nil, fmt.Errorf(gettext.Gettext("unknown remote name: %q"), remote)
	}
	if err := c.Finger(); err != nil {
		return nil, err
	}

	return &c, nil
}

func (c *Client) get(base string) (*Response, error) {
	uri := c.url(APIVersion, base)

	return c.baseGet(uri)
}

func (c *Client) baseGet(url string) (*Response, error) {
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, err
	}

	if c.scert != nil && resp.TLS != nil {
		if !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, c.scert.Raw) {
			return nil, fmt.Errorf(gettext.Gettext("Server certificate has changed"))
		}
	}

	if c.scertDigestSet == false && resp.TLS != nil {
		c.scertWire = resp.TLS.PeerCertificates[0]
		c.scertDigest = sha256.Sum256(resp.TLS.PeerCertificates[0].Raw)
		c.scertDigestSet = true
	}

	return ParseResponse(resp)
}

func (c *Client) put(base string, args Jmap) (*Response, error) {
	uri := c.url(APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	Debugf("putting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("PUT", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	return ParseResponse(resp)
}

func (c *Client) post(base string, args Jmap) (*Response, error) {
	uri := c.url(APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	Debugf("posting %s to %s", buf.String(), uri)

	resp, err := c.http.Post(uri, "application/json", &buf)
	if err != nil {
		return nil, err
	}

	return ParseResponse(resp)
}

func (c *Client) delete(base string, args Jmap) (*Response, error) {
	uri := c.url(APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	Debugf("deleting %s to %s", buf.String(), uri)

	req, err := http.NewRequest("DELETE", uri, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	return ParseResponse(resp)
}

func (c *Client) websocket(operation string, secret string) (*websocket.Conn, error) {
	query := url.Values{"secret": []string{secret}}
	url := c.baseWSURL + path.Join(operation, "websocket") + "?" + query.Encode()
	conn, raw, err := c.websocketDialer.Dial(url, http.Header{})
	if err != nil {
		resp, err2 := ParseResponse(raw)
		if err2 != nil {
			/* The response isn't one we understand, so return
			 * whatever the original error was. */
			return nil, err
		}

		if err2 := ParseError(resp); err2 != nil {
			return nil, err2
		}

		return nil, err
	}
	return conn, err
}

func (c *Client) url(elem ...string) string {
	return c.baseURL + "/" + path.Join(elem...)
}

func unixDial(networ, addr string) (net.Conn, error) {
	var raddr *net.UnixAddr
	var err error
	if addr == "unix.socket:80" {
		raddr, err = net.ResolveUnixAddr("unix", VarPath("unix.socket"))
		if err != nil {
			return nil, fmt.Errorf(gettext.Gettext("cannot resolve unix socket address: %v"), err)
		}
	} else {
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

func (c *Client) GetConfig() (*Response, error) {
	resp, err := c.baseGet(c.url(APIVersion))
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Client) Finger() error {
	Debugf("fingering the daemon")
	resp, err := c.GetConfig()
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

	if serverAPICompat != APICompat {
		return fmt.Errorf(gettext.Gettext("api version mismatch: mine: %q, daemon: %q"), APICompat, serverAPICompat)
	}
	Debugf("pong received")
	return nil
}

func (c *Client) AmTrusted() bool {
	resp, err := c.GetConfig()
	if err != nil {
		return false
	}

	Debugf("%s", resp)

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

func (c *Client) ListContainers() ([]string, error) {
	resp, err := c.get("list")
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Sync {
		return nil, fmt.Errorf(gettext.Gettext("bad response type from list!"))
	}
	var result []string

	if err := json.Unmarshal(resp.Metadata, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *Client) UserAuthServerCert() error {
	if !c.scertDigestSet {
		return fmt.Errorf(gettext.Gettext("No certificate on this connection"))
	}

	if c.scert != nil {
		fmt.Printf(gettext.Gettext("Certificate already stored.\n"))
		return nil
	}

	fmt.Printf(gettext.Gettext("Certificate fingerprint: % x\n"), c.scertDigest)
	fmt.Printf(gettext.Gettext("ok (y/n)? "))
	line, err := ReadStdin()
	if err != nil {
		return err
	}
	if line[0] != 'y' && line[0] != 'Y' {
		return fmt.Errorf(gettext.Gettext("Server certificate NACKed by user"))
	}

	// User acked the cert, now add it to our store
	dnam := configPath("servercerts")
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

func (c *Client) CertificateList() (map[string]string, error) {
	raw, err := c.get("certificates")
	if err != nil {
		return nil, err
	}

	if err := ParseError(raw); err != nil {
		return nil, err
	}

	ret := make(map[string]string)
	if err := json.Unmarshal(raw.Metadata, &ret); err != nil {
		return nil, err
	}

	return ret, nil
}

func (c *Client) AddMyCertToServer(pwd string) error {
	body := Jmap{"type": "client", "password": pwd}

	raw, err := c.post("certificates", body)
	if err != nil {
		return err
	}

	return ParseError(raw)
}

func (c *Client) CertificateAdd(cert *x509.Certificate, name string) error {
	b64 := base64.StdEncoding.EncodeToString(cert.Raw)
	raw, err := c.post("certificates", Jmap{"type": "client", "certificate": b64, "name": name})
	if err != nil {
		return err
	}

	return ParseError(raw)
}

func (c *Client) CertificateRemove(fingerprint string) error {
	raw, err := c.delete(fmt.Sprintf("certificates/%s", fingerprint), nil)
	if err != nil {
		return err
	}
	return ParseError(raw)
}

func (c *Client) Create(name string) (*Response, error) {

	source := Jmap{"type": "remote", "url": "https+lxc-images://images.linuxcontainers.org", "name": "lxc-images/ubuntu/trusty/amd64"}
	body := Jmap{"source": source}

	if name != "" {
		body["name"] = name
	}

	resp, err := c.post("containers", body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Non-async response from create!"))
	}

	return resp, nil
}

func (c *Client) Exec(name string, cmd []string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.WriteCloser) (int, error) {
	body := Jmap{"command": cmd, "wait-for-websocket": true}
	resp, err := c.post(fmt.Sprintf("containers/%s/exec", name), body)
	if err != nil {
		return -1, err
	}

	if err := ParseError(resp); err != nil {
		return -1, err
	}

	if resp.Type != Async {
		return -1, fmt.Errorf(gettext.Gettext("got bad response type from exec"))
	}

	md, err := resp.MetadataAsMap()
	if err != nil {
		return -1, err
	}

	secret, err := md.GetString("websocket_secret")
	if err != nil {
		return -1, err
	}

	conn, err := c.websocket(resp.Operation, secret)
	if err != nil {
		return -1, err
	}

	WebsocketMirror(conn, stdout, stdin)
	op, err := c.WaitFor(resp.Operation)
	if err != nil {
		return -1, err
	}

	if op.StatusCode == Failure {
		return -1, op.GetError()
	}

	if op.StatusCode != Success {
		return -1, fmt.Errorf(gettext.Gettext("got bad op status %s"), op.Status)
	}

	md, err = op.MetadataAsMap()
	if err != nil {
		return -1, err
	}

	return md.GetInt("return")
}

func (c *Client) Action(name string, action ContainerAction, timeout int, force bool) (*Response, error) {
	body := Jmap{"action": action, "timeout": timeout, "force": force}
	resp, err := c.put(fmt.Sprintf("containers/%s/state", name), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Client) Delete(name string) (*Response, error) {
	resp, err := c.delete(fmt.Sprintf("containers/%s", name), nil)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("got non-async response from delete!"))
	}

	return resp, nil
}

func (c *Client) ContainerStatus(name string) (*Container, error) {
	ct := Container{}

	resp, err := c.get(fmt.Sprintf("containers/%s", name))
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Sync {
		return nil, fmt.Errorf(gettext.Gettext("got non-sync response from containers get!"))
	}

	if err := json.Unmarshal(resp.Metadata, &ct); err != nil {
		return nil, err
	}

	return &ct, nil
}

func (c *Client) PushFile(container string, p string, gid int, uid int, mode os.FileMode, buf io.ReadSeeker) error {
	query := url.Values{"path": []string{p}}
	uri := c.url(APIVersion, "containers", container, "files") + "?" + query.Encode()

	req, err := http.NewRequest("PUT", uri, buf)
	if err != nil {
		return err
	}

	req.Header.Set("X-LXD-mode", fmt.Sprintf("%04o", mode))
	req.Header.Set("X-LXD-uid", strconv.FormatUint(uint64(uid), 10))
	req.Header.Set("X-LXD-gid", strconv.FormatUint(uint64(gid), 10))

	raw, err := c.http.Do(req)
	if err != nil {
		return err
	}

	resp, err := ParseResponse(raw)
	if err != nil {
		return err
	}

	return ParseError(resp)
}

func (c *Client) PullFile(container string, p string) (int, int, os.FileMode, io.ReadCloser, error) {
	uri := c.url(APIVersion, "containers", container, "files")
	query := url.Values{"path": []string{p}}

	r, err := c.http.Get(uri + "?" + query.Encode())
	if err != nil {
		return 0, 0, 0, nil, err
	}

	if r.StatusCode != 200 {
		resp, err := ParseResponse(r)
		if err != nil {
			return 0, 0, 0, nil, err
		}

		return 0, 0, 0, nil, ParseError(resp)
	}

	uid, gid, mode, err := ParseLXDFileHeaders(r.Header)
	if err != nil {
		return 0, 0, 0, nil, err
	}

	return uid, gid, mode, r.Body, nil
}

func (c *Client) SetRemotePwd(password string) (*Response, error) {
	body := Jmap{"config": []Jmap{Jmap{"key": "trust-password", "value": password}}}
	resp, err := c.put("", body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

/* Wait for an operation */
func (c *Client) WaitFor(waitURL string) (*Operation, error) {
	/* For convenience, waitURL is expected to be in the form of a
	 * Response.Operation string, i.e. it already has
	 * "/<version>/operations/" in it; we chop off the leading / and pass
	 * it to url directly.
	 */
	Debugf(path.Join(waitURL[1:], "wait"))
	resp, err := c.baseGet(c.url(waitURL, "wait"))
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	return resp.MetadataAsOperation()
}

func (c *Client) WaitForSuccess(waitURL string) error {
	op, err := c.WaitFor(waitURL)
	if err != nil {
		return err
	}

	if op.StatusCode == Success {
		return nil
	}

	return op.GetError()
}

func (c *Client) Snapshot(container string, snapshotName string, stateful bool) (*Response, error) {
	body := Jmap{"name": snapshotName, "stateful": stateful}
	resp, err := c.post(fmt.Sprintf("containers/%s/snapshots", container), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Non-async response from snapshot!"))
	}

	return resp, nil
}
