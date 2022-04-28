package cancel

import (
	"fmt"
	"net/http"
	"sync"
)

// HTTPRequestCanceller tracks a cancelable operation
type HTTPRequestCanceller struct {
	reqChCancel map[*http.Request]chan struct{}
	lock        sync.Mutex
}

// NewHTTPRequestCanceller returns a new HTTPRequestCanceller struct
func NewHTTPRequestCanceller() *HTTPRequestCanceller {
	c := HTTPRequestCanceller{}

	c.lock.Lock()
	c.reqChCancel = make(map[*http.Request]chan struct{})
	c.lock.Unlock()

	return &c
}

// Cancelable indicates whether there are operations that support cancellation
func (c *HTTPRequestCanceller) Cancelable() bool {
	c.lock.Lock()
	length := len(c.reqChCancel)
	c.lock.Unlock()

	return length > 0
}

// Cancel will attempt to cancel all ongoing operations
func (c *HTTPRequestCanceller) Cancel() error {
	if !c.Cancelable() {
		return fmt.Errorf("This operation can't be canceled at this time")
	}

	c.lock.Lock()
	for req, ch := range c.reqChCancel {
		close(ch)
		delete(c.reqChCancel, req)
	}
	c.lock.Unlock()

	return nil
}

// CancelableDownload performs an http request and allows for it to be canceled at any time
func CancelableDownload(c *HTTPRequestCanceller, client *http.Client, req *http.Request) (*http.Response, chan bool, error) {
	chDone := make(chan bool)
	chCancel := make(chan struct{})
	if c != nil {
		c.lock.Lock()
		c.reqChCancel[req] = chCancel
		c.lock.Unlock()
	}
	req.Cancel = chCancel

	go func() {
		<-chDone
		if c != nil {
			c.lock.Lock()
			delete(c.reqChCancel, req)
			c.lock.Unlock()
		}
	}()

	resp, err := client.Do(req)
	if err != nil {
		close(chDone)
		return nil, nil, err
	}

	return resp, chDone, nil
}
