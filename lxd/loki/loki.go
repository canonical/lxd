package loki

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/cancel"
)

// This is a modified version of https://github.com/grafana/loki/blob/v1.6.1/pkg/promtail/client/.

const (
	contentType  = "application/json"
	maxErrMsgLen = 1024
)

type config struct {
	batchSize int
	batchWait time.Duration

	caCert   string
	username string
	password string
	labels   []string
	instance string
	logLevel string
	types    []string
	location string

	timeout time.Duration
	url     *url.URL
}

type entry struct {
	labels LabelSet
	Entry
}

// Client represents a Loki client.
type Client struct {
	cfg     config
	client  *http.Client
	cancel  cancel.Canceller
	entries chan entry
	wg      sync.WaitGroup
}

// NewClient returns a Client.
func NewClient(ctx context.Context, u *url.URL, username string, password string, caCert string, instance string, location string, logLevel string, labels []string, types []string) (*Client, error) {
	client := Client{
		cfg: config{
			batchSize: 10 * 1024,
			batchWait: 1 * time.Second,
			caCert:    caCert,
			username:  username,
			password:  password,
			instance:  instance,
			location:  location,
			labels:    labels,
			logLevel:  logLevel,
			timeout:   10 * time.Second,
			types:     types,
			url:       u,
		},
		client:  &http.Client{},
		entries: make(chan entry),
		cancel:  cancel.New(),
	}

	if caCert != "" {
		tlsConfig, err := shared.GetTLSConfigMem("", "", caCert, "", false)
		if err != nil {
			return nil, err
		}

		client.client.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	} else {
		client.client = http.DefaultClient
	}

	_, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, client.cfg.timeout)
		defer cancel()
	}

	err := client.checkLoki(ctx)
	if err != nil {
		return nil, err
	}

	client.wg.Add(1)
	go client.run()

	return &client, nil
}

func (c *Client) run() {
	batch := newBatch()

	minWaitCheckFrequency := 10 * time.Millisecond
	maxWaitCheckFrequency := max(c.cfg.batchWait/10, minWaitCheckFrequency)

	maxWaitCheck := time.NewTicker(maxWaitCheckFrequency)

	defer func() {
		c.wg.Done()
	}()

	for {
		select {
		case <-c.cancel.Done():
			return

		case e := <-c.entries:
			// If adding the entry to the batch will increase the size over the max
			// size allowed, we do send the current batch and then create a new one
			if batch.sizeBytesAfter(e) > c.cfg.batchSize {
				c.sendBatch(batch)

				batch = newBatch(e)
				break
			}

			// The max size of the batch isn't reached, so we can add the entry
			batch.add(e)

		case <-maxWaitCheck.C:
			// Send batch if max wait time has been reached
			if batch.age() < c.cfg.batchWait {
				break
			}

			c.sendBatch(batch)
			batch = newBatch()
		}
	}
}

func (c *Client) checkLoki(ctx context.Context) error {
	req, err := http.NewRequest(http.MethodGet, c.cfg.url.String()+"/ready", nil)
	if err != nil {
		return err
	}

	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return errors.New("failed to connect to Loki")
	}

	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxErrMsgLen))
		line := ""

		if scanner.Scan() {
			line = scanner.Text()
		}

		return fmt.Errorf("Loki is not ready, server returned HTTP status %s (%d): %s", resp.Status, resp.StatusCode, line)
	}

	return nil
}

func (c *Client) sendBatch(batch *batch) {
	if batch.empty() {
		return
	}

	buf, _, err := batch.encode()
	if err != nil {
		return
	}

	var status int

	for range 30 {
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.timeout)
		status, err = c.send(ctx, buf)
		cancel()

		if err != nil {
			return
		}

		// Only retry 429s, 500s and connection-level errors.
		if status > 0 && status != 429 && status/100 != 5 {
			return
		}

		// Retry every 10s, but exit if Stop() is called.
		select {
		case <-c.cancel.Done():
			return
		case <-time.After(c.cfg.timeout):
		}
	}
}

func (c *Client) send(ctx context.Context, buf []byte) (int, error) {
	req, err := http.NewRequest(http.MethodPost, c.cfg.url.String()+"/loki/api/v1/push", bytes.NewReader(buf))
	if err != nil {
		return -1, err
	}

	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", contentType)

	if c.cfg.username != "" && c.cfg.password != "" {
		req.SetBasicAuth(c.cfg.username, c.cfg.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return -1, err
	}

	if resp.StatusCode/100 != 2 {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxErrMsgLen))
		line := ""

		if scanner.Scan() {
			line = scanner.Text()
		}

		err = fmt.Errorf("server returned HTTP status %s (%d): %s", resp.Status, resp.StatusCode, line)
	}

	return resp.StatusCode, err
}

// Stop the client.
func (c *Client) Stop() {
	c.cancel.Cancel()
	c.wg.Wait()
}

// HandleEvent handles the event received from the internal event listener.
func (c *Client) HandleEvent(event api.Event) {
	if !slices.Contains(c.cfg.types, event.Type) {
		return
	}

	// Support overriding the location field (used on standalone systems).
	location := event.Location
	if c.cfg.location != "" {
		location = c.cfg.location
	}

	entry := entry{
		labels: LabelSet{
			"app":      "lxd",
			"type":     event.Type,
			"location": location,
			"instance": c.cfg.instance,
		},
		Entry: Entry{
			Timestamp: event.Timestamp,
		},
	}

	context := make(map[string]string)

	switch event.Type {
	case api.EventTypeLifecycle:
		lifecycleEvent := api.EventLifecycle{}

		err := json.Unmarshal(event.Metadata, &lifecycleEvent)
		if err != nil {
			return
		}

		if lifecycleEvent.Name != "" {
			entry.labels["name"] = lifecycleEvent.Name
		}

		if lifecycleEvent.Project != "" {
			entry.labels["project"] = lifecycleEvent.Project
		}

		// Build map. These key-value pairs will either be added as labels, or be part of the
		// log message itself.
		context["action"] = lifecycleEvent.Action
		context["source"] = lifecycleEvent.Source

		maps.Copy(context, buildNestedContext("context", lifecycleEvent.Context))

		if lifecycleEvent.Requestor != nil {
			context["requester-address"] = lifecycleEvent.Requestor.Address
			context["requester-protocol"] = lifecycleEvent.Requestor.Protocol
			context["requester-username"] = lifecycleEvent.Requestor.Username
		}

		// Add key-value pairs as labels but don't override any labels.
		for k, v := range context {
			if slices.Contains(c.cfg.labels, k) {
				_, ok := entry.labels[k]
				if !ok {
					// Label names may not contain any hyphens.
					entry.labels[strings.ReplaceAll(k, "-", "_")] = v
					delete(context, k)
				}
			}
		}

		var line strings.Builder

		// Add the remaining context as the message prefix.
		for k, v := range context {
			line.WriteString(k + `="` + v + `" `)
		}

		line.WriteString(lifecycleEvent.Action)

		entry.Line = line.String()
	case api.EventTypeLogging, api.EventTypeOVN:
		logEvent := api.EventLogging{}

		err := json.Unmarshal(event.Metadata, &logEvent)
		if err != nil {
			return
		}

		// The errors can be ignored as the values are validated elsewhere.
		l1, _ := logrus.ParseLevel(logEvent.Level)
		l2, _ := logrus.ParseLevel(c.cfg.logLevel)

		// Only consider log messages with a certain log level.
		if l2 < l1 {
			return
		}

		tmpContext := map[string]any{}

		// Convert map[string]string to map[string]any as buildNestedContext takes the latter type.
		for k, v := range logEvent.Context {
			tmpContext[k] = v
		}

		// Build map. These key-value pairs will either be added as labels, or be part of the
		// log message itself.
		context["level"] = logEvent.Level

		maps.Copy(context, buildNestedContext("context", tmpContext))

		// Add key-value pairs as labels but don't override any labels.
		for k, v := range context {
			if slices.Contains(c.cfg.labels, k) {
				_, ok := entry.labels[k]
				if !ok {
					entry.labels[k] = v
					delete(context, k)
				}
			}
		}

		keys := make([]string, 0, len(context))

		for k := range context {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		var message strings.Builder

		// Add the remaining context as the message prefix. The keys are sorted alphabetically.
		for _, k := range keys {
			message.WriteString(k + `="` + context[k] + `" `)
		}

		message.WriteString(logEvent.Message)

		entry.Line = message.String()
	}

	c.entries <- entry
}

func buildNestedContext(prefix string, m map[string]any) map[string]string {
	labels := map[string]string{}

	for k, v := range m {
		t := reflect.TypeOf(v)

		if t != nil && t.Kind() == reflect.Map {
			for k, v := range buildNestedContext(k, v.(map[string]any)) {
				if prefix == "" {
					labels[k] = v
				} else {
					labels[prefix+"-"+k] = v
				}
			}
		} else {
			if prefix == "" {
				labels[k] = fmt.Sprint(v)
			} else {
				labels[prefix+"-"+k] = fmt.Sprint(v)
			}
		}
	}

	return labels
}

// MarshalJSON returns the JSON encoding of Entry.
func (e Entry) MarshalJSON() ([]byte, error) {
	return []byte(`["` + strconv.FormatInt(e.Timestamp.UnixNano(), 10) + `", ` + strconv.Quote(e.Line) + "]"), nil
}

// String implements the Stringer interface. It returns a formatted/sorted set of label key/value pairs.
func (l LabelSet) String() string {
	var b strings.Builder

	keys := make([]string, 0, len(l))

	for k := range l {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
			b.WriteByte(' ')
		}

		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l[k]))
	}

	b.WriteByte('}')
	return b.String()
}
