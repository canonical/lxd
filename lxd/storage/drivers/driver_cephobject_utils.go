package drivers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/revert"
)

// radosgwadmin wrapper around radosgw-admin command.
func (d *cephobject) radosgwadmin(ctx context.Context, args ...string) (string, error) {
	_, ok := ctx.Deadline()
	if !ok {
		// Set default timeout of 30s if no deadline context provided.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	cmd := make([]string, 0, 5+len(args))
	cmd = append(cmd, "radosgw-admin", "--cluster", d.config["cephobject.cluster_name"], "--id", d.config["cephobject.user.name"])
	cmd = append(cmd, args...)

	return shared.RunCommand(ctx, cmd[0], cmd[1:]...)
}

// radosgwadminGetUser returns credentials for an existing radosgw user (and its sub users).
// If no user exists returns api.StatusError with status code set to http.StatusNotFound.
func (d *cephobject) radosgwadminGetUser(ctx context.Context, user string) (*S3Credentials, map[string]S3Credentials, error) {
	out, err := d.radosgwadmin(ctx, "user", "info", "--uid", user)

	if err != nil {
		status, _ := shared.ExitStatus(err)
		if status == 22 {
			return nil, nil, api.StatusErrorf(http.StatusNotFound, "User not found")
		}

		return nil, nil, fmt.Errorf("Failed getting user %q info: %w", user, err)
	}

	resp := struct {
		SubUsers []struct {
			ID string `json:"id"`
		} `json:"subusers"`
		Keys []struct {
			S3Credentials `yaml:",inline"`
			User          string `json:"user"`
		} `json:"keys"`
	}{}

	err = json.Unmarshal([]byte(out), &resp)
	if err != nil {
		return nil, nil, err
	}

	// Get list of sub user names and store them without the main user prefix.
	subUsers := make(map[string]S3Credentials, len(resp.SubUsers))
	for _, subUser := range resp.SubUsers {
		subUserName := strings.TrimPrefix(subUser.ID, user+":")
		subUsers[subUserName] = S3Credentials{}
	}

	var userKey *S3Credentials

	// Iterate through the keys extracting the main user key and keys for the known sub users.
	for _, key := range resp.Keys {
		if key.User == user {
			userKey = &S3Credentials{
				AccessKey: key.AccessKey,
				SecretKey: key.SecretKey,
			}
		} else {
			for subUserName := range subUsers {
				if strings.TrimPrefix(key.User, user+":") == subUserName {
					subUser := subUsers[subUserName]
					subUser.AccessKey = key.AccessKey
					subUser.SecretKey = key.SecretKey
					subUsers[subUserName] = subUser
				}
			}
		}
	}

	if userKey == nil {
		return nil, nil, fmt.Errorf("S3 credentials not found for %q user", user)
	}

	return userKey, subUsers, nil
}

// radosgwadminUserAdd creates a radosgw user and return generated credentials.
func (d *cephobject) radosgwadminUserAdd(ctx context.Context, user string, maxBuckets int) (*S3Credentials, error) {
	revert := revert.New()
	defer revert.Fail()

	out, err := d.radosgwadmin(ctx, "user", "create", "--max-buckets", strconv.FormatInt(int64(maxBuckets), 10), "--display-name", user, "--uid", user)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = d.radosgwadminUserDelete(ctx, user) })

	creds := struct {
		Keys []struct {
			S3Credentials `yaml:",inline"`
			User          string `json:"user"`
		} `json:"keys"`
	}{}

	err = json.Unmarshal([]byte(out), &creds)
	if err != nil {
		return nil, err
	}

	for _, key := range creds.Keys {
		if key.User == user {
			revert.Success()

			return &S3Credentials{AccessKey: key.AccessKey, SecretKey: key.SecretKey}, err
		}
	}

	return nil, fmt.Errorf("S3 credentials not found for %q user", user)
}

// radosgwadminUserDelete deletes radosgw user.
func (d *cephobject) radosgwadminUserDelete(ctx context.Context, user string) error {
	_, err := d.radosgwadmin(ctx, "user", "rm", "--uid", user, "--purge-data")

	return err
}

// radosgwadminSubUserAdd adds a radosgw sub user.
func (d *cephobject) radosgwadminSubUserAdd(ctx context.Context, user string, subuser string, accessRole string, accessKey string, secretKey string) (*S3Credentials, error) {
	revert := revert.New()
	defer revert.Fail()

	args := []string{"subuser", "create", "--uid", user, "--key-type", "s3", "--subuser", subuser, "--access", accessRole}

	if accessKey == "" {
		args = append(args, "--gen-access-key")
	} else {
		args = append(args, "--access-key", accessKey)
	}

	if secretKey == "" {
		args = append(args, "--gen-secret")
	} else {
		args = append(args, "--secret", secretKey)
	}

	out, err := d.radosgwadmin(ctx, args...)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { _ = d.radosgwadminUserDelete(ctx, user) })

	creds := struct {
		Keys []struct {
			S3Credentials `yaml:",inline"`
			User          string `json:"user"`
		} `json:"keys"`
	}{}

	err = json.Unmarshal([]byte(out), &creds)
	if err != nil {
		return nil, err
	}

	keyUser := user + ":" + subuser

	for _, key := range creds.Keys {
		if key.User == keyUser {
			revert.Success()

			return &S3Credentials{AccessKey: key.AccessKey, SecretKey: key.SecretKey}, err
		}
	}

	return nil, fmt.Errorf("S3 credentials not found for %q user", keyUser)
}

// radosgwadminSubUserDelete deletes radosgw sub-user.
func (d *cephobject) radosgwadminSubUserDelete(ctx context.Context, user string, subuser string) error {
	_, err := d.radosgwadmin(ctx, "subuser", "rm", "--uid", user, "--subuser", subuser)

	return err
}

// s3CreateBucket creates a bucket via the S3 API using an HTTP PUT request with AWS Signature V4 authentication.
func (d *cephobject) s3CreateBucket(ctx context.Context, creds S3Credentials, bucket string) error {
	u, err := url.ParseRequestURI(d.config["cephobject.radosgw.endpoint"])
	if err != nil {
		return fmt.Errorf("Failed parsing cephobject.radosgw.endpoint: %w", err)
	}

	u.Path = path.Join(u.Path, url.PathEscape(bucket))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), nil)
	if err != nil {
		return err
	}

	s3SignRequest(req, creds, "")

	transport, err := d.s3Transport()
	if err != nil {
		return err
	}

	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Failed sending S3 create bucket request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Failed creating S3 bucket (HTTP %d)", resp.StatusCode)
	}

	return nil
}

// s3Transport returns an HTTP transport configured for the radosgw endpoint.
func (d *cephobject) s3Transport() (http.RoundTripper, error) {
	u, err := url.ParseRequestURI(d.config["cephobject.radosgw.endpoint"])
	if err != nil {
		return nil, fmt.Errorf("Failed parsing cephobject.radosgw.endpoint: %w", err)
	}

	certFilePath := d.config["cephobject.radosgw.endpoint_cert_file"]
	if u.Scheme == "https" && certFilePath != "" {
		certFilePath = shared.HostPath(certFilePath)

		certs, err := os.ReadFile(certFilePath)
		if err != nil {
			return nil, fmt.Errorf("Failed reading %q: %w", certFilePath, err)
		}

		rootCAs := x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(certs) {
			return nil, errors.New("Failed adding S3 client certificates")
		}

		return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}, nil
	}

	return http.DefaultTransport, nil
}

// s3SignRequest signs an HTTP request using AWS Signature V4.
func s3SignRequest(req *http.Request, creds S3Credentials, payloadHash string) {
	if payloadHash == "" {
		payloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // SHA-256 of empty string.
	}

	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	region := "us-east-1"
	service := "s3"
	credentialScope := datestamp + "/" + region + "/" + service + "/aws4_request"

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	// Build canonical request.
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalQueryString := req.URL.Query().Encode()
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + req.URL.Host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + amzDate + "\n"

	canonicalRequest := req.Method + "\n" + canonicalURI + "\n" + canonicalQueryString + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash

	// Build string to sign.
	canonicalRequestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + hex.EncodeToString(canonicalRequestHash[:])

	// Calculate signature.
	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+creds.SecretKey), datestamp), region), service), "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	// Set Authorization header.
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+creds.AccessKey+"/"+credentialScope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// hmacSHA256 returns the HMAC-SHA256 of the data using the given key.
func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// radosgwadminBucketDelete deletes radosgw bucket.
func (d *cephobject) radosgwadminBucketDelete(ctx context.Context, bucket string) error {
	_, err := d.radosgwadmin(ctx, "bucket", "rm", "--bucket", bucket, "--purge-objects")

	return err
}

// radosgwadminBucketExists checks if a radosgw bucket exists.
func (d *cephobject) radosgwadminBucketExists(ctx context.Context, bucket string) (bool, error) {
	buckets, err := d.radosgwadminBucketList(ctx)
	if err != nil {
		return false, err
	}

	return slices.Contains(buckets, bucket), nil
}

// radosgwadminBucketLink links a bucket to a user.
func (d *cephobject) radosgwadminBucketLink(ctx context.Context, bucket string, user string) error {
	_, err := d.radosgwadmin(ctx, "bucket", "link", "--bucket", bucket, "--uid", user)

	return err
}

// radosgwadminBucketSetQuota sets bucket quota.
func (d *cephobject) radosgwadminBucketSetQuota(ctx context.Context, user string, size int64) error {
	if size > 0 {
		_, err := d.radosgwadmin(ctx, "quota", "enable", "--quota-scope=bucket", "--uid", user)
		if err != nil {
			return err
		}

		_, err = d.radosgwadmin(ctx, "quota", "set", "--quota-scope=bucket", "--uid", user, "--max-size", strconv.FormatInt(size, 10))
		if err != nil {
			return err
		}
	} else {
		_, err := d.radosgwadmin(ctx, "quota", "disable", "--quota-scope=bucket", "--uid", user)
		if err != nil {
			return err
		}

		_, err = d.radosgwadmin(ctx, "quota", "set", "--quota-scope=bucket", "--uid", user, "--max-size", "-1")
		if err != nil {
			return err
		}
	}

	return nil
}

// radosgwadminBucketList returns the list of buckets.
func (d *cephobject) radosgwadminBucketList(ctx context.Context) ([]string, error) {
	out, err := d.radosgwadmin(ctx, "bucket", "list")
	if err != nil {
		return nil, err
	}

	buckets := []string{}

	err = json.Unmarshal([]byte(out), &buckets)
	if err != nil {
		return nil, err
	}

	return buckets, nil
}

// radosgwBucketName returns the bucket name to use for the actual radosgw bucket.
func (d *cephobject) radosgwBucketName(bucketName string) string {
	return d.config["cephobject.bucket.name_prefix"] + bucketName
}
