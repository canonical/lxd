package miniod

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

type minioAdmin struct {
	alias      string
	configDir  string
	commonArgs []string
}

// AccountInfo is the response body of the info service account call.
type AccountInfo struct {
	ParentUser    string          `json:"parentUser"`
	AccountStatus string          `json:"accountStatus"`
	ImpliedPolicy bool            `json:"impliedPolicy"`
	Policy        json.RawMessage `json:"policy"`
}

// Credentials holds access and secret keys.
type Credentials struct {
	AccessKey    string    `xml:"AccessKeyId"     json:"accessKey"`
	SecretKey    string    `xml:"SecretAccessKey" json:"secretKey"`
	SessionToken string    `xml:"SessionToken"    json:"sessionToken"`
	Expiration   time.Time `xml:"Expiration"      json:"expiration"`
}

// ServiceAccountArgs is the request options for adding or modifying a service account.
type ServiceAccountArgs struct {
	Policy    json.RawMessage `json:"policy"` // Parsed value from iam/policy.Parse()
	AccessKey string          `json:"accessKey"`
	SecretKey string          `json:"secretKey"`
}

// configJSON represents the JSON payload containing the configuration dump.
type configJSON struct {
	Status string `json:"status"`
	Value  []byte `json:"value"`
}

// minioAlias represents the default alias that registers the minio server with the mc command line tool.
// Each call to `NewAdminClient` will refresh this alias.
const minioAlias = "lxd-minio"

// NewAdminClient returns a new AdminClient, and assigns the given credentials to an alias, and returns an `mc` client for that alias.
func NewAdminClient(url string, username string, password string) (*minioAdmin, error) {
	configDir := shared.VarPath("minio")

	m := &minioAdmin{
		alias:      minioAlias,
		configDir:  configDir,
		commonArgs: []string{"--insecure", "--config-dir", configDir},
	}

	args := m.commonArgs
	args = append(args, "alias", "set", m.alias, api.NewURL().Scheme("http").Host(url).String(), username, password)
	_, err := shared.RunCommand("mc", args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to set MinIO client alias: %w", err)
	}

	return m, nil
}

// ServiceStop stops the minio service.
func (m *minioAdmin) ServiceStop(ctx context.Context) error {
	args := m.commonArgs
	args = append(args, "admin", "service", "stop", m.alias)
	_, err := shared.RunCommandContext(ctx, "mc", args...)
	if err != nil {
		return fmt.Errorf("Failed to stop MinIO service: %w", err)
	}

	return nil
}

// GetConfig dumps the minio configuration.
func (m *minioAdmin) GetConfig(ctx context.Context) ([]byte, error) {
	args := m.commonArgs
	args = append(args, "admin", "config", "export", m.alias, "--json")
	out, err := shared.RunCommandContext(ctx, "mc", args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to get MinIO config: %w", err)
	}

	cfg := configJSON{}
	err = json.Unmarshal([]byte(out), &cfg)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse MinIO config: %w", err)
	}

	return cfg.Value, nil
}

// ExportIAM exports IAM data and returns a reader to it.
func (m *minioAdmin) ExportIAM(ctx context.Context) (*zip.Reader, error) {
	name := "mc"
	args := m.commonArgs
	args = append(args, "admin", "cluster", "iam", "export", m.alias)
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	tmpDir, err := os.MkdirTemp(shared.VarPath("storage-pools"), fmt.Sprintf("%s_iam_export_", m.alias))
	if err != nil {
		return nil, err
	}

	// Once we load the exported zip in memory, we won't need to keep it on disk anymore.
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Set the working directory to our tempdir so the zip will be written there.
	cmd.Dir = tmpDir
	err = cmd.Run()
	if err != nil {
		return nil, shared.NewRunError(name, args, err, &stdout, &stderr)
	}

	f, err := os.Open(filepath.Join(tmpDir, fmt.Sprintf("%s-iam-info.zip", m.alias)))
	if err != nil {
		return nil, fmt.Errorf("Failed to open exported IAM information: %w", err)
	}

	iamBytes, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("Failed to read exported IAM information: %w", err)
	}

	iamZipReader, err := zip.NewReader(bytes.NewReader(iamBytes), int64(len(iamBytes)))
	if err != nil {
		return nil, fmt.Errorf("Failed create reader for exported IAM information: %w", err)
	}

	return iamZipReader, nil
}

// InfoServiceAccount gets relevant account information for the service account with the given account key.
func (m *minioAdmin) InfoServiceAccount(ctx context.Context, accessKey string) (*AccountInfo, error) {
	args := m.commonArgs
	args = append(args, "admin", "user", "svcacct", "info", m.alias, accessKey, "--json")
	out, err := shared.RunCommandContext(ctx, "mc", args...)
	if err != nil {
		return nil, err
	}

	info := AccountInfo{}
	err = json.Unmarshal([]byte(out), &info)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse service account information: %w", err)
	}

	return &info, nil
}

// UpdateServiceAccount updates the secret key and/or policy of the service account with the given access key.
func (m *minioAdmin) UpdateServiceAccount(ctx context.Context, opts ServiceAccountArgs) error {
	policyPath := ""
	if len(opts.Policy) > 0 {
		// The mc command can only read the policy from a file, so save it to a temp dir.
		tmpDir, err := os.MkdirTemp(shared.VarPath("storage-pools"), fmt.Sprintf("%s_svcacct_update", m.alias))
		if err != nil {
			return err
		}

		defer func() {
			_ = os.RemoveAll(tmpDir)
		}()

		policyPath := filepath.Join(tmpDir, "policy.json")
		err = os.WriteFile(policyPath, opts.Policy, 0600)
		if err != nil {
			return err
		}
	}

	args := m.commonArgs
	args = append(args, "admin", "user", "svcacct", "edit", m.alias, opts.AccessKey)
	if policyPath != "" {
		args = append(args, "--policy", policyPath)
	}

	if opts.SecretKey != "" {
		args = append(args, "--secret-key", opts.SecretKey)
	}

	_, err := shared.RunCommandContext(ctx, "mc", args...)
	if err != nil {
		return fmt.Errorf("Failed to edit MinIO service account: %w", err)
	}

	return nil
}

// AddServiceAccount creates a new service account with the given args.
func (m *minioAdmin) AddServiceAccount(ctx context.Context, opts ServiceAccountArgs) (*Credentials, error) {
	// The mc command can only read the policy from a file, so save it to a temp dir.
	tmpDir, err := os.MkdirTemp(shared.VarPath("storage-pools"), fmt.Sprintf("%s_svcacct_add", m.alias))
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	policyPath := filepath.Join(tmpDir, "policy.json")
	err = os.WriteFile(policyPath, opts.Policy, 0600)
	if err != nil {
		return nil, err
	}

	args := m.commonArgs
	args = append(args, "admin", "user", "svcacct", "add", m.alias, minioAdminUser, "--access-key", opts.AccessKey, "--secret-key", opts.SecretKey, "--policy", policyPath, "--json")
	out, err := shared.RunCommandContext(ctx, "mc", args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to add MinIO service account: %w", err)
	}

	creds := Credentials{}
	err = json.Unmarshal([]byte(out), &creds)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse service account credentials: %w", err)
	}

	return &creds, nil
}

// DeleteServiceAccount deletes the service account with the given account key.
func (m *minioAdmin) DeleteServiceAccount(ctx context.Context, serviceAccount string) error {
	args := m.commonArgs
	args = append(args, "admin", "user", "svcacct", "remove", m.alias, serviceAccount)
	_, err := shared.RunCommandContext(ctx, "mc", args...)
	if err != nil {
		return fmt.Errorf("Failed to delete MinIO service account: %w", err)
	}

	return nil
}
