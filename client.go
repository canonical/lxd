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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/gorilla/websocket"
	"github.com/gosexy/gettext"
	"github.com/lxc/lxd/shared"
)

// Client can talk to a LXD daemon.
type Client struct {
	config          Config
	Remote          *RemoteConfig
	name            string
	http            http.Client
	baseURL         string
	baseWSURL       string
	certf           string
	keyf            string
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
	Operation string              `json:"operation"`
	Resources map[string][]string `json:"resources"`

	/* Valid only for Error responses */
	Code  int    `json:"error_code"`
	Error string `json:"error"`

	/* Valid for Sync and Error responses */
	Metadata json.RawMessage `json:"metadata"`
}

func IsSnapshot(name string) bool {
	x := strings.SplitN(name, "/", 2)

	if len(x) == 2 {
		return true
	}
	return false
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

func ParseError(r *Response) error {
	if r.Type == Error {
		return fmt.Errorf(r.Error)
	}

	return nil
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
		}

		c.websocketDialer = websocket.Dialer{
			TLSClientConfig: tlsconfig,
		}

		c.certf = certf
		c.keyf = keyf

		c.baseURL = "https://" + r.Addr
		c.baseWSURL = "wss://" + r.Addr
		c.Remote = &r
		c.http.Transport = tr
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
	uri := c.url(shared.APIVersion, base)

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

func (c *Client) put(base string, args shared.Jmap) (*Response, error) {
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
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	return ParseResponse(resp)
}

func (c *Client) post(base string, args shared.Jmap) (*Response, error) {
	uri := c.url(shared.APIVersion, base)

	buf := bytes.Buffer{}
	err := json.NewEncoder(&buf).Encode(args)
	if err != nil {
		return nil, err
	}

	shared.Debugf("posting %s to %s", buf.String(), uri)

	resp, err := c.http.Post(uri, "application/json", &buf)
	if err != nil {
		return nil, err
	}

	return ParseResponse(resp)
}

func (c *Client) delete(base string, args shared.Jmap) (*Response, error) {
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
	return WebsocketDial(c.websocketDialer, url)
}

func (c *Client) url(elem ...string) string {
	return c.baseURL + "/" + path.Join(elem...)
}

func unixDial(networ, addr string) (net.Conn, error) {
	var raddr *net.UnixAddr
	var err error
	if addr == "unix.socket:80" {
		raddr, err = net.ResolveUnixAddr("unix", shared.VarPath("unix.socket"))
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

func (c *Client) GetServerConfig() (*Response, error) {
	resp, err := c.baseGet(c.url(shared.APIVersion))
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	return resp, nil
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

func (c *Client) ListContainers() ([]string, error) {
	resp, err := c.get("containers")
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

	names := []string{}

	for _, url := range result {
		toScan := strings.Replace(url, "/", " ", -1)
		version := ""
		name := ""
		count, err := fmt.Sscanf(toScan, " %s containers %s", &version, &name)
		if err != nil {
			return nil, err
		}

		if count != 2 {
			return nil, fmt.Errorf(gettext.Gettext("bad container url %s"), url)
		}

		if version != shared.APIVersion {
			return nil, fmt.Errorf(gettext.Gettext("bad version in container url"))
		}

		names = append(names, name)
	}

	return names, nil
}

func (c *Client) ExportImage(image string, target string) (*Response, error) {

	uri := c.url(shared.APIVersion, "images", image, "export")

	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	raw, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	// because it is raw data, we need to check for http status
	if raw.StatusCode != 200 {
		resp, err := ParseResponse(raw)
		if err != nil {
			return nil, err
		}
		return nil, ParseError(resp)
	}

	var wr io.Writer

	if target == "-" {
		wr = os.Stdout
	} else if fi, err := os.Stat(target); err == nil {
		// file exists, so check if folder
		switch mode := fi.Mode(); {
		case mode.IsDir():
			// save in directory, header content-disposition can not be null
			// and will have a filename
			cd := strings.Split(raw.Header["Content-Disposition"][0], "=")

			// write filename from header
			f, err := os.Create(filepath.Join(target, cd[1]))
			defer f.Close()

			if err != nil {
				return nil, err
			}

			wr = f

		default:
			// overwrite file
			f, err := os.Open(target)
			defer f.Close()

			if err != nil {
				return nil, err
			}

			wr = f
		}

	} else {

		// write as simple file
		f, err := os.Create(target)
		defer f.Close()

		wr = f
		if err != nil {
			return nil, err
		}

	}

	_, err = io.Copy(wr, raw.Body)

	if err != nil {
		return nil, err
	}

	// it streams to stdout or file, so no response returned
	return nil, nil

}

func (c *Client) PostImage(filename string, properties []string) (*Response, error) {
	uri := c.url(shared.APIVersion, "images")

	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	req, err := http.NewRequest("POST", uri, f)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-LXD-filename", filename)
	mode := 0 // private
	req.Header.Set("X-LXD-public", fmt.Sprintf("%04o", mode))
	//req.Header.Set("X-LXD-fingerprint", fmt.Sprintf("%04o", mode))
	if len(properties) != 0 {
		props := strings.Join(properties, "; ")
		req.Header.Set("X-LXD-properties", props)
	}

	raw, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	resp, err := ParseResponse(raw)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Client) GetImageInfo(image string) (*shared.ImageInfo, error) {
	resp, err := c.get(fmt.Sprintf("images/%s", image))

	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Sync {
		return nil, fmt.Errorf(gettext.Gettext("got non-sync response from containers get!"))
	}

	info := shared.ImageInfo{}
	if err := json.Unmarshal(resp.Metadata, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

func (c *Client) PutImageProperties(name string, p shared.ImageProperties) error {
	body := shared.Jmap{"properties": p}
	resp, err := c.put(fmt.Sprintf("images/%s", name), body)
	if err != nil {
		return err
	}

	if err := ParseError(resp); err != nil {
		return err
	}

	return nil
}

func (c *Client) ListImages() ([]string, error) {
	resp, err := c.get("images")
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

func (c *Client) DeleteImage(image string) error {
	_, err := c.delete(fmt.Sprintf("images/%s", image), nil)
	return err
}

func (c *Client) PostAlias(alias string, desc string, target string) error {
	body := shared.Jmap{"description": desc, "target": target, "name": alias}

	raw, err := c.post("images/aliases", body)
	if err != nil {
		return err
	}
	return ParseError(raw)
}

func (c *Client) DeleteAlias(alias string) error {
	_, err := c.delete(fmt.Sprintf("images/aliases/%s", alias), nil)
	return err
}

func (c *Client) ListAliases() ([]string, error) {
	resp, err := c.get("images/aliases")
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Sync {
		return nil, fmt.Errorf(gettext.Gettext("bad response type from image list!"))
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
	line, err := shared.ReadStdin()
	if err != nil {
		return err
	}
	if line[0] != 'y' && line[0] != 'Y' {
		return fmt.Errorf(gettext.Gettext("Server certificate NACKed by user"))
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
	body := shared.Jmap{"type": "client", "password": pwd}

	raw, err := c.post("certificates", body)
	if err != nil {
		return err
	}

	return ParseError(raw)
}

func (c *Client) CertificateAdd(cert *x509.Certificate, name string) error {
	b64 := base64.StdEncoding.EncodeToString(cert.Raw)
	raw, err := c.post("certificates", shared.Jmap{"type": "client", "certificate": b64, "name": name})
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

func (c *Client) IsAlias(alias string) (bool, error) {
	resp, err := c.get(fmt.Sprintf("images/aliases/%s", alias))
	if err != nil {
		return false, err
	}

	if resp.Type == Error {
		if resp.Code == http.StatusNotFound {
			return false, nil
		} else {
			return false, ParseError(resp)
		}
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
func (c *Client) Init(name string, image string) (*Response, error) {

	source := shared.Jmap{"type": "image"}

	isAlias, err := c.IsAlias(image)
	if err != nil {
		return nil, err
	}

	if isAlias {
		source["alias"] = image
	} else {
		source["fingerprint"] = image
	}

	/* TODO - lxc/init.go should accept --profile arg;  when it does,
	 * we will pass int ["default"] if no profiles listed, "" if an
	 * empty profile was passed, or the passed-in profiles otherwise
	 */
	profiles := []string{"default"}
	body := shared.Jmap{"source": source, "profiles": profiles}

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
		return nil, fmt.Errorf(gettext.Gettext("Non-async response from init!"))
	}

	return resp, nil
}

type execMd struct {
	FDs map[string]string `json:"fds"`
}

func (c *Client) Exec(name string, cmd []string, env map[string]string, stdin *os.File, stdout *os.File, stderr *os.File) (int, error) {
	interactive := terminal.IsTerminal(int(stdin.Fd()))

	body := shared.Jmap{"command": cmd, "wait-for-websocket": true, "interactive": interactive, "environment": env}

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

	md := execMd{}
	if err := json.Unmarshal(resp.Metadata, &md); err != nil {
		return -1, err
	}

	if interactive {
		conn, err := c.websocket(resp.Operation, md.FDs[string(0)])
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
			conns[i], err = c.websocket(resp.Operation, md.FDs[string(i)])
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
	var url string
	s := strings.SplitN(name, "/", 2)
	if len(s) == 2 {
		url = fmt.Sprintf("containers/%s/snapshots/%s", s[0], s[1])
	} else {
		url = fmt.Sprintf("containers/%s", name)
	}
	resp, err := c.delete(url, nil)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Non-async response from delete!"))
	}

	return resp, nil
}

func (c *Client) ContainerStatus(name string) (*shared.ContainerState, error) {
	ct := shared.ContainerState{}

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

func (c *Client) ProfileConfig(name string) (*shared.ProfileConfig, error) {
	ct := shared.ProfileConfig{}

	resp, err := c.get(fmt.Sprintf("profiles/%s", name))
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
	uri := c.url(shared.APIVersion, "containers", container, "files") + "?" + query.Encode()

	req, err := http.NewRequest("POST", uri, buf)
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
	uri := c.url(shared.APIVersion, "containers", container, "files")
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

	uid, gid, mode, err := shared.ParseLXDFileHeaders(r.Header)
	if err != nil {
		return 0, 0, 0, nil, err
	}

	return uid, gid, mode, r.Body, nil
}

func (c *Client) SetRemotePwd(password string) (*Response, error) {
	body := shared.Jmap{"config": []shared.Jmap{shared.Jmap{"key": "trust-password", "value": password}}}
	resp, err := c.put("", body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Client) MigrateTo(container string, target *Client) (*Response, error) {
	body := shared.Jmap{"host": target.Remote.Addr}

	resp, err := c.post(fmt.Sprintf("containers/%s", container), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("got non-async response!"))
	}

	return resp, nil
}

func (c *Client) MigrateFrom(name string, operation string, secrets map[string]string, config map[string]string, profiles []string) (*Response, error) {
	source := shared.Jmap{
		"type":      "migration",
		"mode":      "pull",
		"operation": operation,
		"secrets":   secrets,
	}
	body := shared.Jmap{
		"source":   source,
		"name":     name,
		"config":   config,
		"profiles": profiles,
	}

	resp, err := c.post("containers", body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("got non-async response!"))
	}

	return resp, nil
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

	if op.StatusCode == shared.Success {
		return nil
	}

	return op.GetError()
}

func (c *Client) Snapshot(container string, snapshotName string, stateful bool) (*Response, error) {
	body := shared.Jmap{"name": snapshotName, "stateful": stateful}
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

func (c *Client) ListSnapshots(container string) ([]string, error) {
	qUrl := fmt.Sprintf("containers/%s/snapshots", container)
	resp, err := c.get(qUrl)
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

	names := []string{}

	for _, url := range result {
		// /1.0/containers/<name>/snapshots/<snapshot>
		apart := strings.SplitN(url, "/", 6)
		if len(apart) < 6 {
			return nil, fmt.Errorf(gettext.Gettext("bad container url %s"), url)
		}
		version := apart[1]
		cname := apart[3]
		name := apart[5]

		if cname != container || apart[2] != "containers" || apart[4] != "snapshots" {
			return nil, fmt.Errorf(gettext.Gettext("bad container url %s"), url)
		}

		if version != shared.APIVersion {
			return nil, fmt.Errorf(gettext.Gettext("bad version in container url"))
		}

		names = append(names, name)
	}

	return names, nil
}

/*
 * return string array representing a container's full configuration
 */
func (c *Client) GetContainerConfig(container string) ([]string, error) {
	st, err := c.ContainerStatus(container)
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
	st, err := c.ContainerStatus(container)
	if err != nil {
		return nil, err
	}

	if value == "" {
		delete(st.Config, key)
	} else {
		st.Config[key] = value
	}

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": container, "devices": st.Devices}
	resp, err := c.put(fmt.Sprintf("containers/%s", container), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Unexpected non-async response"))
	}

	return resp, nil
}

func (c *Client) ProfileCreate(p string) error {
	body := shared.Jmap{"name": p}

	raw, err := c.post("profiles", body)
	if err != nil {
		return err
	}
	return ParseError(raw)
}

func (c *Client) ProfileDelete(p string) error {
	_, err := c.delete(fmt.Sprintf("profiles/%s", p), nil)
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
	resp, err := c.put(fmt.Sprintf("profiles/%s", profile), body)
	if err != nil {
		return err
	}

	if err := ParseError(resp); err != nil {
		return err
	}

	if resp.Type != Sync {
		return fmt.Errorf(gettext.Gettext("Unexpected async response"))
	}

	return nil
}

func (c *Client) PutProfile(name string, profile shared.ProfileConfig) error {
	if profile.Name != name {
		return fmt.Errorf(gettext.Gettext("Cannot change profile name"))
	}
	body := shared.Jmap{"name": name, "config": profile.Config, "devices": profile.Devices}
	resp, err := c.put(fmt.Sprintf("profiles/%s", name), body)
	if err != nil {
		return err
	}

	if err := ParseError(resp); err != nil {
		return err
	}

	if resp.Type != Sync {
		return fmt.Errorf(gettext.Gettext("Unexpected async response"))
	}

	return nil
}

func (c *Client) ListProfiles() ([]string, error) {
	resp, err := c.get("profiles")
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
	st, err := c.ContainerStatus(container)
	if err != nil {
		return nil, err
	}
	profiles := strings.Split(profile, ",")
	body := shared.Jmap{"config": st.Config, "profiles": profiles, "name": st.Name, "devices": st.Devices}

	resp, err := c.put(fmt.Sprintf("containers/%s", container), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Unexpected non-async response"))
	}

	return resp, nil
}

func (c *Client) ContainerDeviceDelete(container, devname string) (*Response, error) {
	st, err := c.ContainerStatus(container)
	if err != nil {
		return nil, err
	}

	delete(st.Devices, devname)

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": st.Name, "devices": st.Devices}
	resp, err := c.put(fmt.Sprintf("containers/%s", container), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Unexpected non-async response"))
	}

	return resp, nil
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
			return nil, fmt.Errorf(gettext.Gettext("no value found in %q\n"), p)
		}
		k := results[0]
		v := results[1]
		newdev[k] = v
	}
	newdev["type"] = devtype
	if st.Devices == nil {
		st.Devices = shared.Devices{}
	}
	st.Devices[devname] = newdev

	body := shared.Jmap{"config": st.Config, "profiles": st.Profiles, "name": st.Name, "devices": st.Devices}
	resp, err := c.put(fmt.Sprintf("containers/%s", container), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Async {
		return nil, fmt.Errorf(gettext.Gettext("Unexpected non-async response"))
	}

	return resp, nil
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
	resp, err := c.put(fmt.Sprintf("profiles/%s", profile), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Sync {
		return nil, fmt.Errorf(gettext.Gettext("bad response type from list!"))
	}

	return resp, nil
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
	newdev["type"] = devtype
	if st.Devices == nil {
		st.Devices = shared.Devices{}
	}
	st.Devices[devname] = newdev

	body := shared.Jmap{"config": st.Config, "name": st.Name, "devices": st.Devices}
	resp, err := c.put(fmt.Sprintf("profiles/%s", profile), body)
	if err != nil {
		return nil, err
	}

	if err := ParseError(resp); err != nil {
		return nil, err
	}

	if resp.Type != Sync {
		return nil, fmt.Errorf(gettext.Gettext("bad response type from list!"))
	}

	return resp, nil
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

func (c *Client) ProfileCopy(name, newname string, dest *Client) error {
	st, err := c.ProfileConfig(name)
	if err != nil {
		return err
	}

	body := shared.Jmap{"config": st.Config, "name": newname, "devices": st.Devices}
	resp, err := dest.post("profiles", body)

	if err != nil {
		return err
	}

	if err := ParseError(resp); err != nil {
		return err
	}

	if resp.Type != Sync {
		return fmt.Errorf(gettext.Gettext("bad response type from list!"))
	}

	return nil
}
