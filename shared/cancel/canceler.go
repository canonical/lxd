package cancel

import (
	"fmt"
	"net/http"
)

// A struct to track canceleation
type Canceler struct {
	chCancel chan struct{}
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

func CancelableDownload(c *Canceler, client *http.Client, req *http.Request) (*http.Response, chan bool, error) {
	chDone := make(chan bool)
	chCancel := make(chan struct{})
	if c != nil {
		c.chCancel = chCancel
	}
	req.Cancel = chCancel

	go func() {
		<-chDone
		if c != nil {
			c.chCancel = nil
		}
	}()

	resp, err := client.Do(req)
	return resp, chDone, err
}
