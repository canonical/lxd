package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	lxd "github.com/canonical/lxd/client"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdUI struct {
	global *cmdGlobal
}

// Command is a method of the cmdWebui structure that returns a new cobra Command for displaying resource usage per instance.
func (c *cmdUI) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "ui [<remote>:]"
	cmd.Short = i18n.G("Open the web interface")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Open the web interface`))

	cmd.RunE = c.Run
	return cmd
}

// Run runs the actual command logic.
func (c *cmdUI) Run(cmd *cobra.Command, args []string) error {
	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	remoteName, _, err := c.global.conf.ParseRemote(remote)
	if err != nil {
		return err
	}

	s, err := c.global.conf.GetInstanceServer(remoteName)
	if err != nil {
		return err
	}

	// Create localhost socket.
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("Unable to setup TCP socket: %w", err)
	}

	// Get the connection info.
	info, err := s.GetConnectionInfo()
	if err != nil {
		return err
	}

	uri, err := url.Parse(info.URL)
	if err != nil {
		return err
	}

	// Check that the target supports the UI.
	req, err := http.NewRequest("GET", info.URL+"/ui/", nil)
	if err != nil {
		return err
	}

	resp, err := s.DoHTTP(req)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNotFound {
		return errors.New(i18n.G("The server doesn't have a web UI installed"))
	}

	// Enable keep-alive for proxied connections.
	httpClient, err := s.GetHTTPClient()
	if err != nil {
		return err
	}

	httpTransport, ok := httpClient.Transport.(*http.Transport)
	if ok {
		httpTransport.DisableKeepAlives = false
	}

	// Get server info.
	api10, api10Etag, err := s.GetServer()
	if err != nil {
		return err
	}

	// Generate credentials.
	token := uuid.New().String()

	// Handle inbound connections.
	transport := remoteProxyTransport{
		s:       s,
		baseURL: uri,
	}

	connections := uint64(0)
	transactions := uint64(0)

	handler := remoteProxyHandler{
		s:         s,
		transport: transport,
		api10:     api10,
		api10Etag: api10Etag,

		mu:           &sync.RWMutex{},
		connections:  &connections,
		transactions: &transactions,

		token: token,
	}

	// Print address.
	uiURL := fmt.Sprintf("http://%s/ui?auth_token=%s", server.Addr().String(), token)
	fmt.Printf(i18n.G("Web server running at: %s")+"\n", uiURL)

	// Attempt to automatically open the web browser.
	_ = lxd.OpenBrowser(uiURL)

	// Start the server.
	err = http.Serve(server, handler)
	if err != nil {
		return err
	}

	return nil
}
