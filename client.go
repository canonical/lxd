package lxd

import (
	"crypto/tls"
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
	http    http.Client
	baseURL string
	certf   string
	keyf    string
	cert    tls.Certificate
}

func readMyCert() (string, string, error) {
	homedir := os.Getenv("HOME")
	if homedir == "" {
		return "", "", fmt.Errorf("Failed to find homedir")
	}
	certf := fmt.Sprintf("%s/.config/lxd/%s", homedir, "cert.pem")
	keyf := fmt.Sprintf("%s/.config/lxd/%s", homedir, "key.pem")

	_, err := os.Stat(certf)
	_, err2 := os.Stat(keyf)
	/*
	 * If both stat's succeded, then the cert and pubkey already
	 * exist.
	 */
	if err == nil && err2 == nil {
		return certf, keyf, nil
	}
	/* If one of the stats succeeded and one failed, then there's
	 * a configuration problem, return an error */
	if err == nil {
		Debugf("%s already exists", certf)
		return "", "", err2
	}
	if err2 == nil {
		Debugf("%s already exists", keyf)
		return "", "", err
	}
	/* If neither stat succeeded, then this is our first run and we
	 * need to generate cert and privkey */
	dir := fmt.Sprintf("%s/.config/lxd", homedir)
	err = os.MkdirAll(dir, 0750)
	if err != nil {
		return "", "", err
	}

	Debugf("creating cert: %s %s", certf, keyf)
	err = GenCert(certf, keyf)
	if err != nil {
		return "", "", err
	}
	return certf, keyf, nil
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

	// TODO: Here, we don't support configurable local remotes, we only
	// support the default local lxd at /var/lib/lxd/unix.socket.
	if remote == "" {
		c.baseURL = "http://unix.socket"
		c.http.Transport = &unixTransport
	} else if r, ok := config.Remotes[remote]; ok {
		c.baseURL = "https://" + r.Addr
		c.Remote = &r
	} else {
		return nil, "", fmt.Errorf("unknown remote name: %q", config.DefaultRemote)
	}
	if err := c.Ping(); err != nil {
		return nil, "", err
	}
	return &c, container, nil
}

func (c *Client) getstr(base string, args map[string]string) (string, error) {
	vs := url.Values{}
	for k, v := range args {
		vs.Set(k, v)
	}

	data, err := c.get(base + "?" + vs.Encode())
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) get(elem ...string) ([]byte, error) {
	url := c.url(elem...)
	Debugf("url is %s", url)
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ioutil.ReadAll(resp.Body)
}

func (c *Client) url(elem ...string) string {
	return c.baseURL + path.Join(elem...)
}

var unixTransport = http.Transport{
	Dial: func(network, addr string) (net.Conn, error) {
		if addr != "unix.socket:80" {
			return nil, fmt.Errorf("non-unix-socket addresses not supported yet")
		}
		raddr, err := net.ResolveUnixAddr("unix", VarPath("unix.socket"))
		if err != nil {
			return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
		}
		return net.DialUnix("unix", nil, raddr)
	},
}

// Ping pings the daemon to see if it is up listening and working.
func (c *Client) Ping() error {
	Debugf("pinging the daemon")
	data, err := c.getstr("/ping", nil)
	if err != nil {
		return err
	}

	datav := strings.Split(string(data), " ")
	if datav[0] != Version {
		return fmt.Errorf("version mismatch: mine: %q, daemon: %q", Version, datav[0])
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

func (c *Client) AddCertToServer(pwd string) (string, error) {
	data, err := c.getstr("/trust/add", map[string]string{
		"password": pwd,
	})
	if err != nil {
		return "fail", err
	}
	return data, err
}

func (c *Client) Create(name string, distro string, release string, arch string) (string, error) {
	data, err := c.getstr("/create", map[string]string{
		"name":    name,
		"distro":  distro,
		"release": release,
		"arch":    arch,
	})
	if err != nil {
		return "fail", err
	}
	return data, err
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
