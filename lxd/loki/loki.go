package loki

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grafana/dskit/backoff"
	"github.com/sirupsen/logrus"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// This is a modified version of https://github.com/grafana/loki/blob/v1.6.1/pkg/promtail/client/.

const (
	contentType  = "application/json"
	maxErrMsgLen = 1024
)

type config struct {
	backoffConfig backoff.Config
	batchSize     int
	batchWait     time.Duration
	caCert        string
	username      string
	password      string
	labels        []string
	logLevel      string
	timeout       time.Duration
	types         []string
	url           *url.URL
}

type entry struct {
	labels LabelSet
	Entry
}

// Client represents a Loki client.
type Client struct {
	cfg     config
	client  *http.Client
	ctx     context.Context
	quit    chan struct{}
	once    sync.Once
	entries chan entry
	wg      sync.WaitGroup
}

// NewClient returns a Client.
func NewClient(ctx context.Context, url *url.URL, username string, password string, caCert string, labels []string, logLevel string, types []string) *Client {
	client := Client{
		cfg: config{
			backoffConfig: backoff.Config{
				MinBackoff: 500 * time.Millisecond,
				MaxBackoff: 5 * time.Minute,
				MaxRetries: 10,
			},
			batchSize: 10 * 1024,
			batchWait: 1 * time.Second,
			caCert:    caCert,
			username:  username,
			password:  password,
			labels:    labels,
			logLevel:  logLevel,
			timeout:   10 * time.Second,
			types:     types,
			url:       url,
		},
		client:  &http.Client{},
		ctx:     ctx,
		entries: make(chan entry),
		quit:    make(chan struct{}),
	}

	if caCert != "" {
		tlsConfig, err := shared.GetTLSConfigMem("", "", caCert, "", true)
		if err != nil {
			return nil
		}

		client.client.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	} else {
		client.client = http.DefaultClient
	}

	client.wg.Add(1)
	go client.run()

	return &client
}

// run is the main event loop, it manages entries and sends batches based on configuration.
func (c *Client) run() {
	batch := newBatch()

	minWaitCheckFrequency := 10 * time.Millisecond
	maxWaitCheckFrequency := c.cfg.batchWait / 10

	if maxWaitCheckFrequency < minWaitCheckFrequency {
		maxWaitCheckFrequency = minWaitCheckFrequency
	}

	maxWaitCheck := time.NewTicker(maxWaitCheckFrequency)

	defer func() {
		// Send all pending batches
		c.sendBatch(batch)
		c.wg.Done()
	}()

	for {
		select {
		case <-c.ctx.Done():
			return

		case <-c.quit:
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

// sendBatch encodes and sends a batch of entries,
// retrying on specific errors based on the backoff configuration.
func (c *Client) sendBatch(batch *batch) {
	if batch.empty() {
		return
	}

	buf, _, err := batch.encode()
	if err != nil {
		return
	}

	backoff := backoff.New(c.ctx, c.cfg.backoffConfig)

	var status int

	for backoff.Ongoing() {
		status, err = c.send(c.ctx, buf)
		if err == nil {
			return
		}

		// Only retry 429s, 500s and connection-level errors.
		if status > 0 && status != 429 && status/100 != 5 {
			break
		}

		backoff.Wait()
	}
}

// send creates and sends an HTTP request containing the provided buffer, returning the status code and any error encountered.
func (c *Client) send(ctx context.Context, buf []byte) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.timeout)
	defer cancel()

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/loki/api/v1/push", c.cfg.url.String()), bytes.NewReader(buf))
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
	c.once.Do(func() { close(c.quit) })
	c.wg.Wait()
}

// HandleEvent handles the event received from the internal event listener.
func (c *Client) HandleEvent(event api.Event) {
	if !shared.StringInSlice(event.Type, c.cfg.types) {
		return
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "none"
	}

	entry := entry{
		labels: LabelSet{
			"app":      "lxd",
			"type":     event.Type,
			"location": event.Location,
			"instance": hostname,
		},
		Entry: Entry{
			Timestamp: event.Timestamp,
		},
	}

	context := make(map[string]string)

	if event.Type == api.EventTypeLifecycle {
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

		for k, v := range buildNestedContext("context", lifecycleEvent.Context) {
			context[k] = v
		}

		if lifecycleEvent.Requestor != nil {
			context["requester-address"] = lifecycleEvent.Requestor.Address
			context["requester-protocol"] = lifecycleEvent.Requestor.Protocol
			context["requester-username"] = lifecycleEvent.Requestor.Username
		}

		// Add key-value pairs as labels but don't override any labels.
		for k, v := range context {
			if shared.StringInSlice(k, c.cfg.labels) {
				_, ok := entry.labels[k]
				if !ok {
					// Label names may not contain any hyphens.
					entry.labels[strings.ReplaceAll(k, "-", "_")] = v
					delete(context, k)
				}
			}
		}

		messagePrefix := ""

		// Add the remaining context as the message prefix.
		for k, v := range context {
			messagePrefix += fmt.Sprintf("%s=\"%s\" ", k, v)
		}

		entry.Line = fmt.Sprintf("%s%s", messagePrefix, lifecycleEvent.Action)
	} else if event.Type == api.EventTypeLogging || event.Type == api.EventTypeOVN {
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

		for k, v := range buildNestedContext("context", tmpContext) {
			context[k] = v
		}

		// Add key-value pairs as labels but don't override any labels.
		for k, v := range context {
			if shared.StringInSlice(k, c.cfg.labels) {
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
			message.WriteString(fmt.Sprintf("%s=%q ", k, context[k]))
		}

		message.WriteString(logEvent.Message)

		entry.Line = message.String()
	}

	c.entries <- entry
}

// Generates a flattened key-value map from a nested map, concatenating keys from each level.
func buildNestedContext(prefix string, m map[string]any) map[string]string {
	labels := map[string]string{}

	for k, v := range m {
		t := reflect.TypeOf(v)

		if t != nil && t.Kind() == reflect.Map {
			for k, v := range buildNestedContext(k, v.(map[string]any)) {
				if prefix == "" {
					labels[k] = v
				} else {
					labels[fmt.Sprintf("%s-%s", prefix, k)] = v
				}
			}
		} else {
			if prefix == "" {
				labels[k] = fmt.Sprintf("%v", v)
			} else {
				labels[fmt.Sprintf("%s-%s", prefix, k)] = fmt.Sprintf("%v", v)
			}
		}
	}

	return labels
}

// MarshalJSON returns the JSON encoding of Entry.
func (e Entry) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("[\"%d\", %s]", e.Timestamp.UnixNano(), strconv.Quote(e.Line))), nil
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
