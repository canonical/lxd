package lxd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

// Container handling functions

// GetContainerNames returns a list of container names
func (r *ProtocolLXD) GetContainerNames() ([]string, error) {
	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/containers", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, url := range urls {
		fields := strings.Split(url, "/containers/")
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetContainers returns a list of containers
func (r *ProtocolLXD) GetContainers() ([]api.Container, error) {
	containers := []api.Container{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/containers?recursion=1", nil, "", &containers)
	if err != nil {
		return nil, err
	}

	return containers, nil
}

// GetContainer returns the container entry for the provided name
func (r *ProtocolLXD) GetContainer(name string) (*api.Container, string, error) {
	container := api.Container{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s", name), nil, "", &container)
	if err != nil {
		return nil, "", err
	}

	return &container, etag, nil
}

// CreateContainer requests that LXD creates a new container
func (r *ProtocolLXD) CreateContainer(container api.ContainersPost) (*Operation, error) {
	if container.Source.ContainerOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", "/containers", container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryCreateContainer(req api.ContainersPost, urls []string) (*RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
	}

	rop := RemoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Source.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := []string{}
		for _, serverURL := range urls {
			if operation == "" {
				req.Source.Server = serverURL
			} else {
				req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, operation)
			}

			op, err := r.CreateContainer(req)
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", serverURL, err))
				continue
			}

			rop.targetOp = op

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", serverURL, err))
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = fmt.Errorf("Failed container creation:\n - %s", strings.Join(errors, "\n - "))
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CreateContainerFromImage is a convenience function to make it easier to create a container from an existing image
func (r *ProtocolLXD) CreateContainerFromImage(source ImageServer, image api.Image, req api.ContainersPost) (*RemoteOperation, error) {
	// Set the minimal source fields
	req.Source.Type = "image"

	// Optimization for the local image case
	if r == source {
		// Always use fingerprints for local case
		req.Source.Fingerprint = image.Fingerprint
		req.Source.Alias = ""

		op, err := r.CreateContainer(req)
		if err != nil {
			return nil, err
		}

		rop := RemoteOperation{
			targetOp: op,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Minimal source fields for remote image
	req.Source.Mode = "pull"

	// If we have an alias and the image is public, use that
	if req.Source.Alias != "" && image.Public {
		req.Source.Fingerprint = ""
	} else {
		req.Source.Fingerprint = image.Fingerprint
		req.Source.Alias = ""
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	req.Source.Protocol = info.Protocol
	req.Source.Certificate = info.Certificate

	// Generate secret token if needed
	if !image.Public {
		secret, err := source.GetImageSecret(image.Fingerprint)
		if err != nil {
			return nil, err
		}

		req.Source.Secret = secret
	}

	return r.tryCreateContainer(req, info.Addresses)
}

// CopyContainer copies a container from a remote server. Additional options can be passed using ContainerCopyArgs
func (r *ProtocolLXD) CopyContainer(source ContainerServer, container api.Container, args *ContainerCopyArgs) (*RemoteOperation, error) {
	// Base request
	req := api.ContainersPost{
		Name:         container.Name,
		ContainerPut: container.Writable(),
	}
	req.Source.BaseImage = container.Config["volatile.base_image"]
	req.Source.Live = container.StatusCode == api.Running

	// Process the copy arguments
	if args != nil {
		// Sanity checks
		if args.ContainerOnly && !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}

		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}

		req.Source.Live = args.Live
		req.Source.ContainerOnly = args.ContainerOnly
	}

	// Optimization for the local copy case
	if r == source {
		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = container.Name

		// Copy the container
		op, err := r.CreateContainer(req)
		if err != nil {
			return nil, err
		}

		rop := RemoteOperation{
			targetOp: op,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	// Get a source operation
	sourceReq := api.ContainerPost{
		Migration:     true,
		Live:          req.Source.Live,
		ContainerOnly: req.Source.ContainerOnly,
	}

	op, err := source.MigrateContainer(container.Name, sourceReq)
	if err != nil {
		return nil, err
	}

	sourceSecrets := map[string]string{}
	for k, v := range op.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Migration source fields
	req.Source.Type = "migration"
	req.Source.Mode = "pull"
	req.Source.Operation = op.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateContainer(req, info.Addresses)
}

// UpdateContainer updates the container definition
func (r *ProtocolLXD) UpdateContainer(name string, container api.ContainerPut, ETag string) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("/containers/%s", name), container, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameContainer requests that LXD renames the container
func (r *ProtocolLXD) RenameContainer(name string, container api.ContainerPost) (*Operation, error) {
	// Sanity check
	if container.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameContainer")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s", name), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// MigrateContainer requests that LXD prepares for a container migration
func (r *ProtocolLXD) MigrateContainer(name string, container api.ContainerPost) (*Operation, error) {
	if container.ContainerOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Sanity check
	if !container.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateContainer")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s", name), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteContainer requests that LXD deletes the container
func (r *ProtocolLXD) DeleteContainer(name string) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/containers/%s", name), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// ExecContainer requests that LXD spawns a command inside the container
func (r *ProtocolLXD) ExecContainer(containerName string, exec api.ContainerExecPost, args *ContainerExecArgs) (*Operation, error) {
	if exec.RecordOutput {
		if !r.HasExtension("container_exec_recording") {
			return nil, fmt.Errorf("The server is missing the required \"container_exec_recording\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/exec", containerName), exec, "")
	if err != nil {
		return nil, err
	}

	// Process additional arguments
	if args != nil {
		// Parse the fds
		fds := map[string]string{}

		value, ok := op.Metadata["fds"]
		if ok {
			values := value.(map[string]interface{})
			for k, v := range values {
				fds[k] = v.(string)
			}
		}

		// Call the control handler with a connection to the control socket
		if args.Control != nil && fds["control"] != "" {
			conn, err := r.GetOperationWebsocket(op.ID, fds["control"])
			if err != nil {
				return nil, err
			}

			go args.Control(conn)
		}

		if exec.Interactive {
			// Handle interactive sections
			if args.Stdin != nil && args.Stdout != nil {
				// Connect to the websocket
				conn, err := r.GetOperationWebsocket(op.ID, fds["0"])
				if err != nil {
					return nil, err
				}

				// And attach stdin and stdout to it
				go func() {
					shared.WebsocketSendStream(conn, args.Stdin, -1)
					<-shared.WebsocketRecvStream(args.Stdout, conn)
					conn.Close()

					if args.DataDone != nil {
						close(args.DataDone)
					}
				}()
			} else {
				if args.DataDone != nil {
					close(args.DataDone)
				}
			}
		} else {
			// Handle non-interactive sessions
			dones := map[int]chan bool{}
			conns := []*websocket.Conn{}

			// Handle stdin
			if fds["0"] != "" {
				conn, err := r.GetOperationWebsocket(op.ID, fds["0"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[0] = shared.WebsocketSendStream(conn, args.Stdin, -1)
			}

			// Handle stdout
			if fds["1"] != "" {
				conn, err := r.GetOperationWebsocket(op.ID, fds["1"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[1] = shared.WebsocketRecvStream(args.Stdout, conn)
			}

			// Handle stderr
			if fds["2"] != "" {
				conn, err := r.GetOperationWebsocket(op.ID, fds["2"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[2] = shared.WebsocketRecvStream(args.Stderr, conn)
			}

			// Wait for everything to be done
			go func() {
				for i, chDone := range dones {
					// Skip stdin, dealing with it separately below
					if i == 0 {
						continue
					}

					<-chDone
				}

				if fds["0"] != "" {
					args.Stdin.Close()
				}

				for _, conn := range conns {
					conn.Close()
				}

				if args.DataDone != nil {
					close(args.DataDone)
				}
			}()
		}
	}

	return op, nil
}

// GetContainerFile retrieves the provided path from the container
func (r *ProtocolLXD) GetContainerFile(containerName string, path string) (io.ReadCloser, *ContainerFileResponse, error) {
	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0/containers/%s/files?path=%s", r.httpHost, containerName, path)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("Failed to fetch %s: %s", url, resp.Status)
	}

	// Parse the headers
	uid, gid, mode, fileType, _ := shared.ParseLXDFileHeaders(resp.Header)
	fileResp := ContainerFileResponse{
		UID:  uid,
		GID:  gid,
		Mode: mode,
		Type: fileType,
	}

	if fileResp.Type == "directory" {
		// Decode the response
		response := api.Response{}
		decoder := json.NewDecoder(resp.Body)

		err = decoder.Decode(&response)
		if err != nil {
			return nil, nil, err
		}

		// Get the file list
		entries := []string{}
		err = response.MetadataAsStruct(&entries)
		if err != nil {
			return nil, nil, err
		}

		fileResp.Entries = entries

		return nil, &fileResp, err
	}

	return resp.Body, &fileResp, err
}

// CreateContainerFile tells LXD to create a file in the container
func (r *ProtocolLXD) CreateContainerFile(containerName string, path string, args ContainerFileArgs) error {
	if args.Type == "directory" {
		if !r.HasExtension("directory_manipulation") {
			return fmt.Errorf("The server is missing the required \"directory_manipulation\" API extension")
		}
	}

	if args.WriteMode == "append" {
		if !r.HasExtension("file_append") {
			return fmt.Errorf("The server is missing the required \"file_append\" API extension")
		}
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0/containers/%s/files?path=%s", r.httpHost, containerName, path)
	req, err := http.NewRequest("POST", url, args.Content)
	if err != nil {
		return err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Set the various headers
	if args.UID > -1 {
		req.Header.Set("X-LXD-uid", fmt.Sprintf("%d", args.UID))
	}

	if args.GID > -1 {
		req.Header.Set("X-LXD-gid", fmt.Sprintf("%d", args.GID))
	}

	if args.Mode > -1 {
		req.Header.Set("X-LXD-mode", fmt.Sprintf("%04o", args.Mode))
	}

	if args.Type != "" {
		req.Header.Set("X-LXD-type", args.Type)
	}

	if args.WriteMode != "" {
		req.Header.Set("X-LXD-write", args.WriteMode)
	}

	// Send the request
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Failed to upload to %s: %s", url, resp.Status)
	}

	return nil
}

// DeleteContainerFile deletes a file in the container
func (r *ProtocolLXD) DeleteContainerFile(containerName string, path string) error {
	if !r.HasExtension("file_delete") {
		return fmt.Errorf("The server is missing the required \"file_delete\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/containers/%s/files?path=%s", containerName, path), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetContainerSnapshotNames returns a list of snapshot names for the container
func (r *ProtocolLXD) GetContainerSnapshotNames(containerName string) ([]string, error) {
	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/snapshots", containerName), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, url := range urls {
		fields := strings.Split(url, fmt.Sprintf("/containers/%s/snapshots/", containerName))
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetContainerSnapshots returns a list of snapshots for the container
func (r *ProtocolLXD) GetContainerSnapshots(containerName string) ([]api.ContainerSnapshot, error) {
	snapshots := []api.ContainerSnapshot{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/snapshots?recursion=1", containerName), nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetContainerSnapshot returns a Snapshot struct for the provided container and snapshot names
func (r *ProtocolLXD) GetContainerSnapshot(containerName string, name string) (*api.ContainerSnapshot, string, error) {
	snapshot := api.ContainerSnapshot{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/snapshots/%s", containerName, name), nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// CreateContainerSnapshot requests that LXD creates a new snapshot for the container
func (r *ProtocolLXD) CreateContainerSnapshot(containerName string, snapshot api.ContainerSnapshotsPost) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/snapshots", containerName), snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CopyContainerSnapshot copies a snapshot from a remote server into a new container. Additional options can be passed using ContainerCopyArgs
func (r *ProtocolLXD) CopyContainerSnapshot(source ContainerServer, snapshot api.ContainerSnapshot, args *ContainerSnapshotCopyArgs) (*RemoteOperation, error) {
	// Base request
	fields := strings.SplitN(snapshot.Name, shared.SnapshotDelimiter, 2)
	cName := fields[0]
	sName := fields[1]

	req := api.ContainersPost{
		Name: cName,
		ContainerPut: api.ContainerPut{
			Architecture: snapshot.Architecture,
			Config:       snapshot.Config,
			Devices:      snapshot.Devices,
			Ephemeral:    snapshot.Ephemeral,
			Profiles:     snapshot.Profiles,
			Stateful:     snapshot.Stateful,
		},
	}
	req.Source.BaseImage = snapshot.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}
	}

	// Optimization for the local copy case
	if r == source {
		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = snapshot.Name

		// Copy the container
		op, err := r.CreateContainer(req)
		if err != nil {
			return nil, err
		}

		rop := RemoteOperation{
			targetOp: op,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	// Get a source operation
	sourceReq := api.ContainerSnapshotPost{
		Migration: true,
	}

	op, err := source.MigrateContainerSnapshot(cName, sName, sourceReq)
	if err != nil {
		return nil, err
	}

	sourceSecrets := map[string]string{}
	for k, v := range op.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Migration source fields
	req.Source.Type = "migration"
	req.Source.Mode = "pull"
	req.Source.Operation = op.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateContainer(req, info.Addresses)
}

// RenameContainerSnapshot requests that LXD renames the snapshot
func (r *ProtocolLXD) RenameContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPost) (*Operation, error) {
	// Sanity check
	if container.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameContainerSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/snapshots/%s", containerName, name), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// MigrateContainerSnapshot requests that LXD prepares for a snapshot migration
func (r *ProtocolLXD) MigrateContainerSnapshot(containerName string, name string, container api.ContainerSnapshotPost) (*Operation, error) {
	// Sanity check
	if !container.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateContainerSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/containers/%s/snapshots/%s", containerName, name), container, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteContainerSnapshot requests that LXD deletes the container snapshot
func (r *ProtocolLXD) DeleteContainerSnapshot(containerName string, name string) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("/containers/%s/snapshots/%s", containerName, name), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetContainerState returns a ContainerState entry for the provided container name
func (r *ProtocolLXD) GetContainerState(name string) (*api.ContainerState, string, error) {
	state := api.ContainerState{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/state", name), nil, "", &state)
	if err != nil {
		return nil, "", err
	}

	return &state, etag, nil
}

// UpdateContainerState updates the container to match the requested state
func (r *ProtocolLXD) UpdateContainerState(name string, state api.ContainerStatePut, ETag string) (*Operation, error) {
	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("/containers/%s/state", name), state, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetContainerLogfiles returns a list of logfiles for the container
func (r *ProtocolLXD) GetContainerLogfiles(name string) ([]string, error) {
	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/containers/%s/logs", name), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	logfiles := []string{}
	for _, url := range logfiles {
		fields := strings.Split(url, fmt.Sprintf("/containers/%s/logs/", name))
		logfiles = append(logfiles, fields[len(fields)-1])
	}

	return logfiles, nil
}

// GetContainerLogfile returns the content of the requested logfile
//
// Note that it's the caller's responsibility to close the returned ReadCloser
func (r *ProtocolLXD) GetContainerLogfile(name string, filename string) (io.ReadCloser, error) {
	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0/containers/%s/logs/%s", r.httpHost, name, filename)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Failed to fetch %s: %s", url, resp.Status)
	}

	return resp.Body, err
}

// DeleteContainerLogfile deletes the requested logfile
func (r *ProtocolLXD) DeleteContainerLogfile(name string, filename string) error {
	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/containers/%s/logs/%s", name, filename), nil, "")
	if err != nil {
		return err
	}

	return nil
}
