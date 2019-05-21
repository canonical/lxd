package rafthttp

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CanonicalLtd/raft-membership"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// ChangeMembership can be used to join or leave a cluster over HTTP.
func ChangeMembership(
	kind raftmembership.ChangeRequestKind,
	path string,
	dial Dial,
	id raft.ServerID,
	address, target string,
	timeout time.Duration) error {
	url := makeURL(path)
	url.RawQuery = fmt.Sprintf("id=%s", id)
	if kind == raftmembership.JoinRequest {
		url.RawQuery += fmt.Sprintf("&address=%s", address)
	}
	url.Host = target
	url.Scheme = "http"
	method := membershipChangeRequestKindToMethod[kind]
	request := &http.Request{
		Method:     method,
		URL:        url,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}

	remaining := timeout
	var response *http.Response
	var err error
	for remaining > 0 {
		start := time.Now()
		netDial := func(network, addr string) (net.Conn, error) {
			return dial(addr, remaining)
		}
		client := &http.Client{
			Timeout:   remaining,
			Transport: &http.Transport{Dial: netDial},
		}
		response, err = client.Do(request)

		// If we got a system or network error, just return it.
		if err != nil {
			break
		}

		// If we got an HTTP error, let's capture its details,
		// and possibly return it if it's not retriable or we
		// have hit our timeout.
		if response.StatusCode != http.StatusOK {
			body, _ := ioutil.ReadAll(response.Body)
			err = fmt.Errorf(
				"http code %d '%s'", response.StatusCode,
				strings.TrimSpace(string(body)))
		}
		// If there's a temporary failure, let's retry.
		if response.StatusCode == http.StatusServiceUnavailable {
			// XXX TODO: use an exponential backoff
			// relative to the timeout?
			time.Sleep(100 * time.Millisecond)

			remaining -= time.Since(start)
			continue
		}

		break
	}
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("server %s failed", kind))
	}
	return nil
}

// Build a full url.URL object out of our path.
func makeURL(path string) *url.URL {
	url, err := url.Parse(path)
	if err != nil {
		panic(fmt.Sprintf("invalid URL path %s", path))
	}
	return url
}
