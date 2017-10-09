package cancel

import (
	"fmt"
	"net/http"
)

// A struct to track canceleation
type Canceler struct {
	reqChCancel map[*http.Request]chan struct{}
}

func NewCanceler() *Canceler {
	c := Canceler{}
	c.reqChCancel = make(map[*http.Request]chan struct{})
	return &c
}

func (c *Canceler) Cancelable() bool {
	return len(c.reqChCancel) > 0
}

func (c *Canceler) Cancel() error {
	if !c.Cancelable() {
		return fmt.Errorf("This operation cannot be canceled at this time")
	}

	for req, ch := range c.reqChCancel {
		close(ch)
		delete(c.reqChCancel, req)
	}
	return nil
}

func CancelableDownload(c *Canceler, client *http.Client, req *http.Request) (*http.Response, chan bool, error) {
	chDone := make(chan bool)
	chCancel := make(chan struct{})
	if c != nil {
		c.reqChCancel[req] = chCancel
	}
	req.Cancel = chCancel

	go func() {
		<-chDone
		if c != nil {
			delete(c.reqChCancel, req)
		}
	}()

	resp, err := client.Do(req)
	return resp, chDone, err
}
