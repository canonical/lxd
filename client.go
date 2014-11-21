package lxd

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// Client can talk to a lxd daemon.
type Client struct {
	config  Config
	Remote  *RemoteConfig
	name    string
	http    http.Client
	baseURL string
	certf   string
	keyf    string
	cert    tls.Certificate

	scert *x509.Certificate // the cert stored on disk

	scert_wire       *x509.Certificate // the cert from the tls connection
	scert_digest     [sha256.Size]byte // fingerprint of server cert from connection
	scert_digest_set bool              // whether we've stored the fingerprint
}

type ResponseType string

const (
	Sync  = "sync"
	Async = "async"
	Error = "error"
)

type Response struct {
	Type ResponseType

	/* Valid only for Sync responses */
	Result bool

	/* Valid only for Async responses */
	Operation string

	/* Valid only for Error responses */
	Code  int
	Error string

	/* Valid for Sync and Error responses */
	Metadata Jmap
}

func ParseResponse(r *http.Response) (*Response, error) {
	defer r.Body.Close()
	ret := Response{}
	raw := Jmap{}

	/* We could potentially remove this later, but it is quite handy for
	 * debugging what is actually going on in the client */
	s, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	Debugf("raw response: %s", string(s))

	if err := json.NewDecoder(bytes.NewReader(s)).Decode(&raw); err != nil {
		return nil, err
	}

	Debugf("response: %s", raw)

	if key, ok := raw["type"]; !ok {
		return nil, fmt.Errorf("Response was missing `type`")
	} else if key == Sync {
		ret.Type = Sync

		if result, err := raw.GetString("result"); err != nil {
			return nil, err
		} else if result == "success" {
			ret.Result = true
		} else if result == "failure" {
			ret.Result = false
		} else {
			return nil, fmt.Errorf("Invalid result %s", result)
		}

		if raw["metadata"] == nil {
			ret.Metadata = nil
		} else {
			ret.Metadata = raw["metadata"].(map[string]interface{})
		}

	} else if key == Async {
		ret.Type = Async

		if operation, err := raw.GetString("operation"); err != nil {
			return nil, err
		} else {
			ret.Operation = operation
		}

	} else if key == Error {
		ret.Type = Error

		if code, err := raw.GetInt("error_code"); err != nil {
			return nil, err
		} else {
			ret.Code = code
			if ret.Code != r.StatusCode {
				return nil, fmt.Errorf("response codes don't match! %d %d", ret.Code, r.StatusCode)
			}
		}

		errorStr, err := raw.GetString("error")
		if err != nil {
			return nil, fmt.Errorf("response didn't have error")
		}
		ret.Error = errorStr

		if raw["metadata"] != nil {
			ret.Metadata = raw["metadata"].(map[string]interface{})
		} else {
			ret.Metadata = nil
		}

	} else {
		return nil, fmt.Errorf("Bad response type")
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
	homedir := os.Getenv("HOME")
	if homedir == "" {
		return "", "", fmt.Errorf("Failed to find homedir")
	}
	certf := fmt.Sprintf("%s/.config/lxd/%s", homedir, "cert.pem")
	keyf := fmt.Sprintf("%s/.config/lxd/%s", homedir, "key.pem")

	err := FindOrGenCert(certf, keyf)

	return certf, keyf, err
}

/*
 * load the server cert from disk
 */
func (c *Client) loadServerCert() {
	homedir := os.Getenv("HOME")
	if homedir == "" {
		return
	}
	dnam := fmt.Sprintf("%s/.config/lxd/servercerts", homedir)
	err := os.MkdirAll(dnam, 0750)
	if err != nil {
		return
	}
	fnam := fmt.Sprintf("%s/%s.cert", dnam, c.name)
	cf, err := ioutil.ReadFile(fnam)
	if err != nil {
		return
	}

	cert, err := x509.ParseCertificate(cf)
	if err != nil {
		fmt.Printf("Error reading the server certificate for %s\n", c.name)
		return
	}
	c.scert = cert
}

// NewClient returns a new lxd client.
func NewClient(config *Config, raw string) (*Client, string, error) {
	certf, keyf, err := readMyCert()
	if err != nil {
		return nil, "", err
	}
	cert, err := tls.LoadX509KeyPair(certf, keyf)
	if err != nil {
		return nil, "", err
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

	result := strings.SplitN(raw, ":", 2)
	var remote string
	var container string

	if len(result) == 1 {
		remote = config.DefaultRemote
		container = result[0]
	} else {
		remote = result[0]
		container = result[1]
	}
	c.name = remote

	// TODO: Here, we don't support configurable local remotes, we only
	// support the default local lxd at /var/lib/lxd/unix.socket.
	if remote == "" {
		c.baseURL = "http://unix.socket"
		c.http.Transport = &unixTransport
	} else if len(remote) > 6 && remote[0:5] == "unix:" {
		c.baseURL = "http://unix.socket"
		c.http.Transport = &unixTransport
	} else if r, ok := config.Remotes[remote]; ok {
		c.baseURL = "https://" + r.Addr
		c.Remote = &r
		c.loadServerCert()
	} else {
		return nil, "", fmt.Errorf("unknown remote name: %q", config.DefaultRemote)
	}
	if err := c.Ping(); err != nil {
		return nil, "", err
	}

	return &c, container, nil
}

/* This will be deleted once everything is ported to the new Response framework */
func (c *Client) getstr(base string, args map[string]string) (string, error) {
	vs := url.Values{}
	for k, v := range args {
		vs.Set(k, v)
	}

	resp, err := c.getRawLegacy(base + "?" + vs.Encode())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (c *Client) get(base string) (*Response, error) {
	uri := c.url(ApiVersion, base)

	resp, err := c.http.Get(uri)
	if err != nil {
		return nil, err
	}

	if c.scert != nil && resp.TLS != nil {
		if !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, c.scert.Raw) {
			return nil, fmt.Errorf("Server certificate has changed")
		}
	}

	if c.scert_digest_set == false && resp.TLS != nil {
		c.scert_wire = resp.TLS.PeerCertificates[0]
		c.scert_digest = sha256.Sum256(resp.TLS.PeerCertificates[0].Raw)
		c.scert_digest_set = true
	}

	return ParseResponse(resp)
}

func (c *Client) post(base string, args Jmap) (*Response, error) {
	uri := c.url(ApiVersion, base)

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

func (c *Client) getRawLegacy(elem ...string) (*http.Response, error) {
	url := c.url(elem...)
	Debugf("url is %s", url)
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) url(elem ...string) string {
	return c.baseURL + "/" + path.Join(elem...)
}

var unixTransport = http.Transport{
	Dial: func(network, addr string) (net.Conn, error) {
		var raddr *net.UnixAddr
		var err error
		if addr == "unix.socket:80" {
			raddr, err = net.ResolveUnixAddr("unix", VarPath("unix.socket"))
			if err != nil {
				return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
			}
		} else {
			raddr, err = net.ResolveUnixAddr("unix", addr)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
			}
		}
		return net.DialUnix("unix", nil, raddr)
	},
}

// Ping pings the daemon to see if it is up listening and working.
func (c *Client) Ping() error {
	Debugf("pinging the daemon")
	resp, err := c.get("ping")
	if err != nil {
		return err
	}

	serverApiCompat, err := resp.Metadata.GetInt("api_compat")
	if err != nil {
		return err
	}

	if serverApiCompat != ApiCompat {
		return fmt.Errorf("api version mismatch: mine: %q, daemon: %q", ApiCompat, serverApiCompat)
	}
	Debugf("pong received")
	return nil
}

func (c *Client) AmTrusted() bool {
	data, err := c.getstr("/ping", nil)
	if err != nil {
		return false
	}

	datav := strings.Split(string(data), " ")
	if datav[1] == "trusted" {
		return true
	}
	return false
}

func (c *Client) List() (string, error) {
	data, err := c.getstr("/list", nil)
	if err != nil {
		return "fail", err
	}
	return data, err
}

func (c *Client) UserAuthServerCert() error {
	if !c.scert_digest_set {
		return fmt.Errorf("No certificate on this connection")
	}

	fmt.Printf("Certificate fingerprint: % x\n", c.scert_digest)
	fmt.Printf("ok (y/n)?")
	line, err := ReadStdin()
	if err != nil {
		return err
	}
	if line[0] != 'y' && line[0] != 'Y' {
		return fmt.Errorf("Server certificate NACKed by user")
	}

	// User acked the cert, now add it to our store
	homedir := os.Getenv("HOME")
	if homedir == "" {
		return fmt.Errorf("Could not find homedir")
	}
	dnam := fmt.Sprintf("%s/.config/lxd/servercerts", homedir)
	err = os.MkdirAll(dnam, 0750)
	if err != nil {
		return fmt.Errorf("Could not create server cert dir")
	}
	certf := fmt.Sprintf("%s/%s.cert", dnam, c.name)
	certOut, err := os.Create(certf)
	if err != nil {
		return err
	}
	_, err = certOut.Write(c.scert_wire.Raw)
	certOut.Close()
	return err
}

func (c *Client) AddCertToServer(pwd string) (string, error) {
	data, err := c.getstr("/trust/add", map[string]string{
		"password": pwd,
	})
	if err != nil {
		return "fail", err
	}
	return data, err
}

func (c *Client) Create(name string) (string, error) {

	source := Jmap{"type": "remote", "url": "https+lxc-images://images.linuxcontainers.org", "name": "lxc-images/ubuntu/trusty/amd64"}
	body := Jmap{"source": source}

	if name != "" {
		body["name"] = name
	}

	resp, err := c.post("containers", body)
	if err != nil {
		return "", err
	}

	if err := ParseError(resp); err != nil {
		return "", err
	}

	if resp.Type != Async {
		return "", fmt.Errorf("Non-async response from create!")
	}

	return resp.Operation, nil
}

func (c *Client) Shell(name string, cmd string, secret string) (string, error) {
	data, err := c.getstr("/shell", map[string]string{
		"name":    name,
		"command": cmd,
		"secret":  secret,
	})
	if err != nil {
		return "fail", err
	}
	return data, err
}

// Call a function in the lxd API by name (i.e. this has nothing to do with
// the parameter passing schemed :)
func (c *Client) CallByName(function string, name string) (string, error) {
	data, err := c.getstr("/"+function, map[string]string{"name": name})
	if err != nil {
		return "", err
	}
	return data, err
}

func (c *Client) Delete(name string) (string, error) {
	return c.CallByName("delete", name)
}

func (c *Client) Start(name string) (string, error) {
	return c.CallByName("start", name)
}

func (c *Client) Stop(name string) (string, error) {
	return c.CallByName("stop", name)
}

func (c *Client) Restart(name string) (string, error) {
	return c.CallByName("restart", name)
}

func (c *Client) SetRemotePwd(password string) (string, error) {
	return c.getstr("/trust", map[string]string{
		"password": password,
	})
}
