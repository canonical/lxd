package cookiejar

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"reflect"
	"unsafe"

	"github.com/canonical/lxd/shared/api"
)

// Open opens a new jar at the given location, reading contents from disk if present.
// The cookie jar will only save cookies that set for the given remote address.
func Open(filepath string, remoteAddress string) (*Jar, error) {
	jar, err := newJar(filepath, remoteAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed to open jar: %w", err)
	}

	err = jar.openJar()
	if err != nil {
		return nil, fmt.Errorf("Failed to open jar: %w", err)
	}

	return jar, nil
}

// newJar creates a new [Jar] but does not attempt to read existing contents of the jar.
func newJar(filepath string, remoteAddress string) (*Jar, error) {
	jar, err := cookiejar.New(&cookiejar.Options{})
	if err != nil {
		return nil, fmt.Errorf("Failed to instantiate a cookie jar: %w", err)
	}

	remoteURL, err := url.Parse(remoteAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse remote URL: %w", err)
	}

	return &Jar{Jar: jar, filepath: filepath, remoteAddress: *remoteURL}, nil
}

// Jar implements [http.CookieJar] by embedding a [*cookiejar.Jar].
//
// It accesses the private fields of [*cookiejar.Jar] via reflection to read/write the contents to/from a JSON file.
type Jar struct {
	filepath      string
	remoteAddress url.URL

	*cookiejar.Jar
}

// openJar opens the jar file and obtains a read lock on it. Then reads the contents into the jar and unlocks the file.
// If no file is present this is a no-op.
func (j *Jar) openJar() error {
	// Check if the file exists.
	info, err := os.Stat(j.filepath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Failed to get cookie jar file info: %w", err)
		}

		// If file doesn't exist, nothing to do.
		return nil
	}

	// File cannot be a directory.
	if info.IsDir() {
		return fmt.Errorf("Failed to get a cookie jar file: Given file path %q is a directory", j.filepath)
	}

	// Open file
	f, err := os.Open(j.filepath)
	if err != nil {
		return fmt.Errorf("Failed to open cookie jar file: %w", err)
	}

	defer f.Close()

	// Obtain a read lock.
	err = rLockFile(f)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotImplemented) {
		return fmt.Errorf("Failed to obtain read lock on cookie jar file: %w", err)
	}

	// Release the lock when finished.
	defer func() {
		// Ignore errors from unlocking the file because a) there's nothing we can do and b) the file will be
		// automatically unlocked when the process exits.
		_ = unlockFile(f)
	}()

	// Read the contents into this Jar.
	err = json.NewDecoder(f).Decode(&j)
	if err != nil {
		return fmt.Errorf("Failed to read cookie jar: %w", err)
	}

	return nil
}

// lock obtains a lock on the embedded [cookiejar.Jar].
func (j *Jar) lock() {
	_ = j.mu("Lock")
}

// unlock releases a lock on the embedded [cookiejar.Jar].
func (j *Jar) unlock() {
	_ = j.mu("Unlock")
}

// field gets a private field from the embedded [cookiejar.Jar] and makes it accessible.
func (j *Jar) field(name string) reflect.Value {
	f := reflect.ValueOf(j.Jar).Elem().FieldByName(name)
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr()))
}

// mu calls the given method name on the [sync.Mutex] of the embedded [cookiejar.Jar].
func (j *Jar) mu(method string) []reflect.Value {
	return j.field("mu").MethodByName(method).Call(nil)
}

// MarshalJSON implements [json.Marshaler] for [Jar] by marshaling the "entries" field of the embedded [cookiejar.Jar].
// It obtains a lock on the embedded jar before reading the entries field.
func (j *Jar) MarshalJSON() ([]byte, error) {
	j.lock()
	defer j.unlock()

	return json.Marshal(j.field("entries").Interface())
}

// UnmarshalJSON implements [json.Unmarshaler] for [Jar] by unmarshaling the bytes into the "entries" field of the
// embedded [cookiejar.Jar]. It obtains a lock on the embedded jar before writing to it. It will fail if the jar is not
// empty.
func (j *Jar) UnmarshalJSON(b []byte) error {
	j.lock()
	defer j.unlock()
	if j.field("entries").Elem().Len() != 0 {
		return errors.New("Cannot unmarshal into non-empty cookie jar")
	}

	return json.Unmarshal(b, j.field("entries").Interface())
}

// Save saves the contents of the embedded [cookiejar.Jar] to the file.
// It obtains a write lock on the file before writing the contents.
// A lock on the contents of the [cookiejar.Jar] is obtained when marshaling the JSON.
func (j *Jar) Save() error {
	// Create the file, or truncate it if it already exists. We are fully overwriting the contents.
	f, err := os.Create(j.filepath)
	if err != nil {
		return fmt.Errorf("Failed to create cookie jar file: %w", err)
	}

	defer func() {
		_ = f.Close()
	}()

	// Get a write lock.
	err = lockFile(f)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotImplemented) {
		return fmt.Errorf("Failed to obtain write lock on cookie jar file: %w", err)
	}

	// Release the lock when finished.
	defer func() {
		// Ignore errors from unlocking the file because a) there's nothing we can do and b) the file will be
		// automatically unlocked when the process exits.
		_ = unlockFile(f)
	}()

	// Write the cookie jar contents to the file.
	err = json.NewEncoder(f).Encode(j)
	if err != nil {
		return fmt.Errorf("Failed to save cookies: %w", err)
	}

	return nil
}

// SetCookies implements [http.CookieJar.SetCookies]. It only accepts cookies from the remote host.
func (j *Jar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	// Only accept cookies from the remote host.
	host := u.Hostname()
	allowedHost := j.remoteAddress.Hostname()

	if host == allowedHost {
		j.Jar.SetCookies(u, cookies)
	}
}
