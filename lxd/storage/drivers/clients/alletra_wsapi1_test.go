package clients

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_AlletraClient_sessionKeyCache(t *testing.T) {
	client := &AlletraClient{url: "https://127.0.0.1/alletra-test", username: "user", password: "password"}
	otherClient := &AlletraClient{url: "https://127.0.0.1/alletra-test", username: "user", password: "password"}
	defer client.invalidateSessionKey()

	client.cacheSessionKey("first")
	sessionKey, ok := otherClient.getSessionKey()
	assert.True(t, ok)
	assert.Equal(t, "first", sessionKey)

	client.invalidateSessionKey()
	_, ok = otherClient.getSessionKey()
	assert.False(t, ok)

	var waitGroup sync.WaitGroup
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for range 100 {
				client.cacheSessionKey("first")
				_, _ = otherClient.getSessionKey()
				client.invalidateSessionKey()
			}
		}()
	}

	waitGroup.Wait()
}
