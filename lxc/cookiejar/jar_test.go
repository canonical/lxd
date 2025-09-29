//go:build linux || darwin

package cookiejar

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type cookieJarSuite struct {
	suite.Suite
	j *Jar
}

func TestCookieJarSuite(t *testing.T) {
	suite.Run(t, new(cookieJarSuite))
}

func (s *cookieJarSuite) SetupTest() {
	tmpDir := s.T().TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.json")
	remote := "https://127.0.0.1:8443/"
	j, err := Open(cookieFile, remote)
	s.Require().NoError(err)
	s.j = j
}

func (s *cookieJarSuite) TestSetCookies() {
	require := s.Require()

	// --- Domain example ---
	domainHost := "example.com"
	uRemote, err := url.Parse("https://" + domainHost)
	require.NoError(err)

	uSubdomain, err := url.Parse("https://foo." + domainHost)
	require.NoError(err)

	uOther, err := url.Parse("https://evil.com/")
	require.NoError(err)

	cookies := []*http.Cookie{
		{Name: "foo", Value: "bar"},
	}

	// Should set cookies for remote domain
	s.j.remoteAddress = *uRemote
	s.j.SetCookies(uRemote, cookies)
	got := s.j.Cookies(uRemote)
	require.Len(got, 1)
	require.Equal("foo", got[0].Name)
	require.Equal("bar", got[0].Value)

	// Should not set cookies for subdomain
	s.j.SetCookies(uSubdomain, cookies)
	gotSub := s.j.Cookies(uSubdomain)
	require.Empty(gotSub)

	// Should not set cookies for other domain
	s.j.SetCookies(uOther, cookies)
	gotOther := s.j.Cookies(uOther)
	require.Empty(gotOther)

	// --- IP example ---
	uIPOther, err := url.Parse("http://192.168.1.1/")
	require.NoError(err)

	uIPSub, err := url.Parse("http://sub.127.0.0.1:8443/")
	require.NoError(err)

	ipCookies := []*http.Cookie{
		{Name: "ipcookie", Value: "123"},
	}

	// Remote IP URL
	uIPRemote, err := url.Parse("http://127.0.0.1:8443/")
	require.NoError(err)
	s.j.remoteAddress = *uIPRemote

	// Exact IP allowed
	s.j.SetCookies(uIPRemote, ipCookies)
	gotIP := s.j.Cookies(uIPRemote)
	require.Len(gotIP, 1)
	require.Equal("ipcookie", gotIP[0].Name)

	// Other IP should be rejected
	s.j.SetCookies(uIPOther, ipCookies)
	gotIPOther := s.j.Cookies(uIPOther)
	require.Empty(gotIPOther)

	// Subdomain of IP should be rejected
	s.j.SetCookies(uIPSub, ipCookies)
	gotIPSub := s.j.Cookies(uIPSub)
	require.Empty(gotIPSub)
}

func (s *cookieJarSuite) TestLocking() {
	require := s.Require()

	// Lock the jar (in memory).
	s.j.lock()

	// TryLock should fail.
	values := s.j.mu("TryLock")
	require.Len(values, 1)
	require.Equal(false, values[0].Interface())

	// Unlock the jar (in memory).
	s.j.unlock()

	// TryLock should succeed.
	values = s.j.mu("TryLock")
	require.Len(values, 1)
	require.Equal(true, values[0].Interface())

	// TryLock should fail (because previous TryLock succeeded).
	values = s.j.mu("TryLock")
	require.Len(values, 1)
	require.Equal(false, values[0].Interface())

	// Unlock.
	s.j.unlock()
}

func (s *cookieJarSuite) TestJar() {
	require := s.Require()

	// URL for testing against.
	u, err := url.Parse("https://127.0.0.1:8443/")
	require.NoError(err)

	// Test cookies.
	testCookies := []*http.Cookie{
		{
			Name:     "bacon",
			Value:    "eggs",
			Path:     "/",
			MaxAge:   20,
			Secure:   true,
			HttpOnly: true,
			Quoted:   true,
			SameSite: http.SameSiteStrictMode,
		},
		{
			Name:     "tomato",
			Value:    "beans",
			Path:     "/secret",
			MaxAge:   20,
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		},
		{
			Name:   "shouldnotbehere",
			Value:  "evil",
			Domain: "evil.com",
		},
		{
			Name:   "shouldalsonotbehere",
			Value:  "evil",
			Domain: "1.2.3.4:5678",
		},
	}

	// Set cookies on the suite jar.
	s.j.SetCookies(u, testCookies)

	// Save the cookies.
	err = s.j.Save()
	require.NoError(err)

	// Open cookie file and obtain a write lock on it.
	f, err := os.Open(s.j.filepath)
	require.NoError(err)

	err = lockFile(f)
	require.NoError(err)

	// Try to open a new jar on the write locked file.
	errCh := make(chan error)
	go func(ch chan<- error) {
		_, err = Open(s.j.filepath, s.j.remoteAddress.String())
		ch <- err
	}(errCh)

	// It blocks until the file is unlocked.
	select {
	case <-errCh:
		s.Fail("Read from locked file")
	case <-time.After(200 * time.Millisecond):
		err = unlockFile(f)
		require.NoError(err)
	}

	err = <-errCh
	require.NoError(err)

	// Unlocking the file again has no effect.
	err = unlockFile(f)
	require.NoError(err)

	// Obtain a read lock on the file.
	err = rLockFile(f)
	require.NoError(err)

	// Should open without error.
	j2, err := Open(s.j.filepath, s.j.remoteAddress.String())
	require.NoError(err)

	// Test that the new jar contains the same cookies as the old jar.
	// Only get one cookie back because the url does not have a path.
	gotCookies1 := j2.Cookies(u)
	require.Len(gotCookies1, 1)

	// Match name, value, and "quoted", other fields aren't set.
	require.Equal(testCookies[0].Name, gotCookies1[0].Name)
	require.Equal(testCookies[0].Value, gotCookies1[0].Value)
	require.True(gotCookies1[0].Quoted)

	// Set the url path to the path of the second cookie.
	u.Path = "/secret"

	// Should now get both cookies back, because the first cookie matches all paths.
	gotCookies2 := j2.Cookies(u)
	require.Len(gotCookies2, 2)

	// Match name, value, and "quoted", other fields aren't set.
	require.Equal(gotCookies1[0], gotCookies2[1])
	require.Equal(testCookies[1].Name, gotCookies2[0].Name)
	require.Equal(testCookies[1].Value, gotCookies2[0].Value)
	require.False(gotCookies2[0].Quoted)

	// Try to save the second jar while the read lock is still held.
	errCh2 := make(chan error)
	go func(ch chan<- error) {
		ch <- j2.Save()
	}(errCh2)

	// It blocks until the file is unlocked.
	select {
	case <-errCh2:
		s.Fail("Read from locked file")
	case <-time.After(200 * time.Millisecond):
		err = unlockFile(f)
		require.NoError(err)
	}

	err = <-errCh2
	require.NoError(err)
}
