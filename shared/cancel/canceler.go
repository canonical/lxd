package cancel

import (
	"fmt"
	"net/http"
)

// A struct to track canceleation
type Canceler struct {
	chCancel chan bool
}

func (c *Canceler) Cancelable() bool {
	return c.chCancel != nil
}

func (c *Canceler) Cancel() error {
	if c.chCancel == nil {
		return fmt.Errorf("This operation cannot be canceled at this time")
	}

	close(c.chCancel)
	c.chCancel = nil
	return nil
}

func CancelableDownload(c *Canceler, client *http.Client, req *http.Request) (*http.Response, error, chan bool) {
	chDone := make(chan bool)

	go func() {
		chCancel := make(chan bool)
		if c != nil {
			c.chCancel = chCancel
		}

		select {
		case <-chCancel:
			if transport, ok := client.Transport.(*http.Transport); ok {
				transport.CancelRequest(req)
			}
		case <-chDone:
		}

		if c != nil {
			c.chCancel = nil
		}
	}()

	resp, err := client.Do(req)
	return resp, err, chDone
}
