package lxd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/cancel"
	"github.com/lxc/lxd/shared/ioprogress"
	"github.com/lxc/lxd/shared/units"
)

// Instance handling functions.

// instanceTypeToPath converts the instance type to a URL path prefix and query string values.
// If the remote server doesn't have the instances extension then the /containers endpoint is used
// as long as the requested instanceType is any or container.
func (r *ProtocolLXD) instanceTypeToPath(instanceType api.InstanceType) (string, url.Values, error) {
	v := url.Values{}

	// If the remote server doesn't support instances extension, check that only containers
	// or any type has been requested and then fallback to using the old /containers endpoint.
	if !r.HasExtension("instances") {
		if instanceType == api.InstanceTypeContainer || instanceType == api.InstanceTypeAny {
			return "/containers", v, nil
		}

		return "", v, fmt.Errorf("Requested instance type not supported by server")
	}

	// If a specific instance type has been requested, add the instance-type filter parameter
	// to the returned URL values so that it can be used in the final URL if needed to filter
	// the result set being returned.
	if instanceType != api.InstanceTypeAny {
		v.Set("instance-type", string(instanceType))
	}

	return "/instances", v, nil
}

// GetInstanceNames returns a list of instance names.
func (r *ProtocolLXD) GetInstanceNames(instanceType api.InstanceType) ([]string, error) {
	urls := []string{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	prefix := path + "/"
	for _, url := range urls {
		fields := strings.Split(url, prefix)
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetInstances returns a list of instances.
func (r *ProtocolLXD) GetInstances(instanceType api.InstanceType) ([]api.Instance, error) {
	instances := []api.Instance{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "1")

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstancesFull returns a list of instances including snapshots, backups and state.
func (r *ProtocolLXD) GetInstancesFull(instanceType api.InstanceType) ([]api.InstanceFull, error) {
	instances := []api.InstanceFull{}

	path, v, err := r.instanceTypeToPath(instanceType)
	if err != nil {
		return nil, err
	}

	v.Set("recursion", "2")

	if !r.HasExtension("container_full") {
		return nil, fmt.Errorf("The server is missing the required \"container_full\" API extension")
	}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s?%s", path, v.Encode()), nil, "", &instances)
	if err != nil {
		return nil, err
	}

	return instances, nil
}

// GetInstance returns the instance entry for the provided name.
func (r *ProtocolLXD) GetInstance(name string) (*api.Instance, string, error) {
	instance := api.Instance{}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), nil, "", &instance)
	if err != nil {
		return nil, "", err
	}

	return &instance, etag, nil
}

// CreateInstanceFromBackup is a convenience function to make it easier to
// create a instance from a backup
func (r *ProtocolLXD) CreateInstanceFromBackup(args InstanceBackupArgs) (Operation, error) {
	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if args.PoolName == "" {
		// Send the request
		op, _, err := r.queryOperation("POST", path, args.BackupFile, "")
		if err != nil {
			return nil, err
		}

		return op, nil
	}

	if !r.HasExtension("container_backup_override_pool") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup_override_pool\" API extension")
	}

	// Prepare the HTTP request
	reqURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0%s", r.httpHost, path))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", reqURL, args.BackupFile)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-LXD-pool", args.PoolName)

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle errors
	response, _, err := lxdParseResponse(resp)
	if err != nil {
		return nil, err
	}

	// Get to the operation
	respOperation, err := response.MetadataAsOperation()
	if err != nil {
		return nil, err
	}

	// Setup an Operation wrapper
	op := operation{
		Operation: *respOperation,
		r:         r,
		chActive:  make(chan bool),
	}

	return &op, nil
}

// CreateInstance requests that LXD creates a new instance.
func (r *ProtocolLXD) CreateInstance(instance api.InstancesPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(instance.Type)
	if err != nil {
		return nil, err
	}

	if instance.Source.InstanceOnly || instance.Source.ContainerOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", path, instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryCreateInstance(req api.InstancesPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The source server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Source.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := map[string]error{}
		for _, serverURL := range urls {
			if operation == "" {
				req.Source.Server = serverURL
			} else {
				req.Source.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))
			}

			op, err := r.CreateInstance(req)
			if err != nil {
				errors[serverURL] = err
				continue
			}

			rop.targetOp = op

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors[serverURL] = err
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed instance creation", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// CreateInstanceFromImage is a convenience function to make it easier to create a instance from an existing image.
func (r *ProtocolLXD) CreateInstanceFromImage(source ImageServer, image api.Image, req api.InstancesPost) (RemoteOperation, error) {
	// Set the minimal source fields
	req.Source.Type = "image"

	// Optimization for the local image case
	if r == source {
		// Always use fingerprints for local case
		req.Source.Fingerprint = image.Fingerprint
		req.Source.Alias = ""

		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		rop := remoteOperation{
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

	return r.tryCreateInstance(req, info.Addresses)
}

// CopyInstance copies a instance from a remote server. Additional options can be passed using InstanceCopyArgs.
func (r *ProtocolLXD) CopyInstance(source InstanceServer, instance api.Instance, args *InstanceCopyArgs) (RemoteOperation, error) {
	// Base request
	req := api.InstancesPost{
		Name:        instance.Name,
		InstancePut: instance.Writable(),
	}
	req.Source.BaseImage = instance.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Sanity checks
		if args.InstanceOnly {
			if !r.HasExtension("container_only_migration") {
				return nil, fmt.Errorf("The target server is missing the required \"container_only_migration\" API extension")
			}

			if !source.HasExtension("container_only_migration") {
				return nil, fmt.Errorf("The source server is missing the required \"container_only_migration\" API extension")
			}
		}

		if shared.StringInSlice(args.Mode, []string{"push", "relay"}) {
			if !r.HasExtension("container_push") {
				return nil, fmt.Errorf("The target server is missing the required \"container_push\" API extension")
			}

			if !source.HasExtension("container_push") {
				return nil, fmt.Errorf("The source server is missing the required \"container_push\" API extension")
			}
		}

		if args.Mode == "push" && !source.HasExtension("container_push_target") {
			return nil, fmt.Errorf("The source server is missing the required \"container_push_target\" API extension")
		}

		if args.Refresh {
			if !r.HasExtension("container_incremental_copy") {
				return nil, fmt.Errorf("The target server is missing the required \"container_incremental_copy\" API extension")
			}

			if !source.HasExtension("container_incremental_copy") {
				return nil, fmt.Errorf("The source server is missing the required \"container_incremental_copy\" API extension")
			}
		}

		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}

		req.Source.Live = args.Live
		req.Source.InstanceOnly = args.InstanceOnly
		req.Source.ContainerOnly = args.InstanceOnly // For legacy servers.
		req.Source.Refresh = args.Refresh
	}

	if req.Source.Live {
		req.Source.Live = instance.StatusCode == api.Running
	}

	sourceInfo, err := source.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get source connection info: %v", err)
	}

	destInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get destination connection info: %v", err)
	}

	// Optimization for the local copy case
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (!r.IsClustered() || instance.Location == r.clusterTarget || r.HasExtension("cluster_internal_copy")) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			if !r.HasExtension("container_copy_project") {
				return nil, fmt.Errorf("The server is missing the required \"container_copy_project\" API extension")
			}

			req.Source.Project = sourceInfo.Project
		}

		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = instance.Name

		// Copy the instance
		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		rop := remoteOperation{
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

	// Source request
	sourceReq := api.InstancePost{
		Migration:     true,
		Live:          req.Source.Live,
		ContainerOnly: req.Source.ContainerOnly, // Deprecated, use InstanceOnly.
		InstanceOnly:  req.Source.InstanceOnly,
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the instance
		req.Source.Type = "migration"
		req.Source.Mode = "push"
		req.Source.Refresh = args.Refresh

		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.InstancePostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateInstance(source, instance.Name, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	op, err := source.MigrateInstance(instance.Name, sourceReq)
	if err != nil {
		return nil, err
	}
	opAPI := op.Get()

	sourceSecrets := map[string]string{}
	for k, v := range opAPI.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Start the process
		targetOp, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}
		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Launch the relay
		err = r.proxyMigration(targetOp.(*operation), targetSecrets, source, op.(*operation), sourceSecrets)
		if err != nil {
			return nil, err
		}

		// Prepare a tracking operation
		rop := remoteOperation{
			targetOp: targetOp,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Pull mode migration
	req.Source.Type = "migration"
	req.Source.Mode = "pull"
	req.Source.Operation = opAPI.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateInstance(req, info.Addresses)
}

func (r *ProtocolLXD) proxyInstanceMigration(targetOp *operation, targetSecrets map[string]string, source InstanceServer, sourceOp *operation, sourceSecrets map[string]string) error {
	// Sanity checks
	for n := range targetSecrets {
		_, ok := sourceSecrets[n]
		if !ok {
			return fmt.Errorf("Migration target expects the \"%s\" socket but source isn't providing it", n)
		}
	}

	if targetSecrets["control"] == "" {
		return fmt.Errorf("Migration target didn't setup the required \"control\" socket")
	}

	// Struct used to hold everything together
	type proxy struct {
		done       chan bool
		sourceConn *websocket.Conn
		targetConn *websocket.Conn
	}

	proxies := map[string]*proxy{}

	// Connect the control socket
	sourceConn, err := source.GetOperationWebsocket(sourceOp.ID, sourceSecrets["control"])
	if err != nil {
		return err
	}

	targetConn, err := r.GetOperationWebsocket(targetOp.ID, targetSecrets["control"])
	if err != nil {
		return err
	}

	proxies["control"] = &proxy{
		done:       shared.WebsocketProxy(sourceConn, targetConn),
		sourceConn: sourceConn,
		targetConn: targetConn,
	}

	// Connect the data sockets
	for name := range sourceSecrets {
		if name == "control" {
			continue
		}

		// Handle resets (used for multiple objects)
		sourceConn, err := source.GetOperationWebsocket(sourceOp.ID, sourceSecrets[name])
		if err != nil {
			break
		}

		targetConn, err := r.GetOperationWebsocket(targetOp.ID, targetSecrets[name])
		if err != nil {
			break
		}

		proxies[name] = &proxy{
			sourceConn: sourceConn,
			targetConn: targetConn,
			done:       shared.WebsocketProxy(sourceConn, targetConn),
		}
	}

	// Cleanup once everything is done
	go func() {
		// Wait for control socket
		<-proxies["control"].done
		proxies["control"].sourceConn.Close()
		proxies["control"].targetConn.Close()

		// Then deal with the others
		for name, proxy := range proxies {
			if name == "control" {
				continue
			}

			<-proxy.done
			proxy.sourceConn.Close()
			proxy.targetConn.Close()
		}
	}()

	return nil
}

// UpdateInstance updates the instance definition.
func (r *ProtocolLXD) UpdateInstance(name string, instance api.InstancePut, ETag string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), instance, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameInstance requests that LXD renames the instance.
func (r *ProtocolLXD) RenameInstance(name string, instance api.InstancePost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Sanity check
	if instance.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameInstance")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryMigrateInstance(source InstanceServer, name string, req api.InstancePost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The target server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Target.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := map[string]error{}
		for _, serverURL := range urls {
			req.Target.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))

			op, err := source.MigrateInstance(name, req)
			if err != nil {
				errors[serverURL] = err
				continue
			}

			rop.targetOp = op

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors[serverURL] = err
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed instance migration", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// MigrateInstance requests that LXD prepares for a instance migration.
func (r *ProtocolLXD) MigrateInstance(name string, instance api.InstancePost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if instance.InstanceOnly || instance.ContainerOnly {
		if !r.HasExtension("container_only_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_only_migration\" API extension")
		}
	}

	// Sanity check
	if !instance.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateInstance")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteInstance requests that LXD deletes the instance.
func (r *ProtocolLXD) DeleteInstance(name string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("%s/%s", path, url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// ExecInstance requests that LXD spawns a command inside the instance.
func (r *ProtocolLXD) ExecInstance(instanceName string, exec api.InstanceExecPost, args *InstanceExecArgs) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if exec.RecordOutput {
		if !r.HasExtension("container_exec_recording") {
			return nil, fmt.Errorf("The server is missing the required \"container_exec_recording\" API extension")
		}
	}

	if exec.User > 0 || exec.Group > 0 || exec.Cwd != "" {
		if !r.HasExtension("container_exec_user_group_cwd") {
			return nil, fmt.Errorf("The server is missing the required \"container_exec_user_group_cwd\" API extension")
		}
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/exec", path, url.PathEscape(instanceName)), exec, "")
	if err != nil {
		return nil, err
	}
	opAPI := op.Get()

	// Process additional arguments
	if args != nil {
		// Parse the fds
		fds := map[string]string{}

		value, ok := opAPI.Metadata["fds"]
		if ok {
			values := value.(map[string]interface{})
			for k, v := range values {
				fds[k] = v.(string)
			}
		}

		// Call the control handler with a connection to the control socket
		if args.Control != nil && fds["control"] != "" {
			conn, err := r.GetOperationWebsocket(opAPI.ID, fds["control"])
			if err != nil {
				return nil, err
			}

			go args.Control(conn)
		}

		if exec.Interactive {
			// Handle interactive sections
			if args.Stdin != nil && args.Stdout != nil {
				// Connect to the websocket
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
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
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[0] = shared.WebsocketSendStream(conn, args.Stdin, -1)
			}

			// Handle stdout
			if fds["1"] != "" {
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["1"])
				if err != nil {
					return nil, err
				}

				conns = append(conns, conn)
				dones[1] = shared.WebsocketRecvStream(args.Stdout, conn)
			}

			// Handle stderr
			if fds["2"] != "" {
				conn, err := r.GetOperationWebsocket(opAPI.ID, fds["2"])
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
					if args.Stdin != nil {
						args.Stdin.Close()
					}

					// Empty the stdin channel but don't block on it as
					// stdin may be stuck in Read()
					go func() {
						<-dones[0]
					}()
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

// GetInstanceFile retrieves the provided path from the instance.
func (r *ProtocolLXD) GetInstanceFile(instanceName string, filePath string) (io.ReadCloser, *InstanceFileResponse, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, nil, err
	}

	// Prepare the HTTP request
	requestURL, err := shared.URLEncode(
		fmt.Sprintf("%s/1.0%s/%s/files", r.httpHost, path, url.PathEscape(instanceName)),
		map[string]string{"path": filePath})
	if err != nil {
		return nil, nil, err
	}

	requestURL, err = r.setQueryAttributes(requestURL)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return nil, nil, err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.do(req)
	if err != nil {
		return nil, nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, nil, err
		}
	}

	// Parse the headers
	uid, gid, mode, fileType, _ := shared.ParseLXDFileHeaders(resp.Header)
	fileResp := InstanceFileResponse{
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

// CreateInstanceFile tells LXD to create a file in the instance.
func (r *ProtocolLXD) CreateInstanceFile(instanceName string, filePath string, args InstanceFileArgs) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if args.Type == "directory" {
		if !r.HasExtension("directory_manipulation") {
			return fmt.Errorf("The server is missing the required \"directory_manipulation\" API extension")
		}
	}

	if args.Type == "symlink" {
		if !r.HasExtension("file_symlinks") {
			return fmt.Errorf("The server is missing the required \"file_symlinks\" API extension")
		}
	}

	if args.WriteMode == "append" {
		if !r.HasExtension("file_append") {
			return fmt.Errorf("The server is missing the required \"file_append\" API extension")
		}
	}

	// Prepare the HTTP request
	requestURL := fmt.Sprintf("%s/1.0%s/%s/files?path=%s", r.httpHost, path, url.PathEscape(instanceName), url.QueryEscape(filePath))

	requestURL, err = r.setQueryAttributes(requestURL)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", requestURL, args.Content)
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
	resp, err := r.do(req)
	if err != nil {
		return err
	}

	// Check the return value for a cleaner error
	_, _, err = lxdParseResponse(resp)
	if err != nil {
		return err
	}

	return nil
}

// DeleteInstanceFile deletes a file in the instance.
func (r *ProtocolLXD) DeleteInstanceFile(instanceName string, filePath string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("file_delete") {
		return fmt.Errorf("The server is missing the required \"file_delete\" API extension")
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/files?path=%s", path, url.PathEscape(instanceName), url.QueryEscape(filePath)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceSnapshotNames returns a list of snapshot names for the instance.
func (r *ProtocolLXD) GetInstanceSnapshotNames(instanceName string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	urls := []string{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/snapshots", path, url.PathEscape(instanceName)), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, uri := range urls {
		fields := strings.Split(uri, fmt.Sprintf("%s/%s/snapshots/", path, url.PathEscape(instanceName)))
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetInstanceSnapshots returns a list of snapshots for the instance.
func (r *ProtocolLXD) GetInstanceSnapshots(instanceName string) ([]api.InstanceSnapshot, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	snapshots := []api.InstanceSnapshot{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/snapshots?recursion=1", path, url.PathEscape(instanceName)), nil, "", &snapshots)
	if err != nil {
		return nil, err
	}

	return snapshots, nil
}

// GetInstanceSnapshot returns a Snapshot struct for the provided instance and snapshot names
func (r *ProtocolLXD) GetInstanceSnapshot(instanceName string, name string) (*api.InstanceSnapshot, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	snapshot := api.InstanceSnapshot{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "", &snapshot)
	if err != nil {
		return nil, "", err
	}

	return &snapshot, etag, nil
}

// CreateInstanceSnapshot requests that LXD creates a new snapshot for the instance.
func (r *ProtocolLXD) CreateInstanceSnapshot(instanceName string, snapshot api.InstanceSnapshotsPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Validate the request
	if snapshot.ExpiresAt != nil && !r.HasExtension("snapshot_expiry_creation") {
		return nil, fmt.Errorf("The server is missing the required \"snapshot_expiry_creation\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/snapshots", path, url.PathEscape(instanceName)), snapshot, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// CopyInstanceSnapshot copies a snapshot from a remote server into a new instance. Additional options can be passed using InstanceCopyArgs.
func (r *ProtocolLXD) CopyInstanceSnapshot(source InstanceServer, instanceName string, snapshot api.InstanceSnapshot, args *InstanceSnapshotCopyArgs) (RemoteOperation, error) {
	// Backward compatibility (with broken Name field)
	fields := strings.Split(snapshot.Name, shared.SnapshotDelimiter)
	cName := instanceName
	sName := fields[len(fields)-1]

	// Base request
	req := api.InstancesPost{
		Name: cName,
		InstancePut: api.InstancePut{
			Architecture: snapshot.Architecture,
			Config:       snapshot.Config,
			Devices:      snapshot.Devices,
			Ephemeral:    snapshot.Ephemeral,
			Profiles:     snapshot.Profiles,
		},
	}

	if snapshot.Stateful && args.Live {
		if !r.HasExtension("container_snapshot_stateful_migration") {
			return nil, fmt.Errorf("The server is missing the required \"container_snapshot_stateful_migration\" API extension")
		}
		req.InstancePut.Stateful = snapshot.Stateful
		req.Source.Live = args.Live
	}
	req.Source.BaseImage = snapshot.Config["volatile.base_image"]

	// Process the copy arguments
	if args != nil {
		// Sanity checks
		if shared.StringInSlice(args.Mode, []string{"push", "relay"}) {
			if !r.HasExtension("container_push") {
				return nil, fmt.Errorf("The target server is missing the required \"container_push\" API extension")
			}

			if !source.HasExtension("container_push") {
				return nil, fmt.Errorf("The source server is missing the required \"container_push\" API extension")
			}
		}

		if args.Mode == "push" && !source.HasExtension("container_push_target") {
			return nil, fmt.Errorf("The source server is missing the required \"container_push_target\" API extension")
		}

		// Allow overriding the target name
		if args.Name != "" {
			req.Name = args.Name
		}
	}

	sourceInfo, err := source.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get source connection info: %v", err)
	}

	destInfo, err := r.GetConnectionInfo()
	if err != nil {
		return nil, fmt.Errorf("Failed to get destination connection info: %v", err)
	}

	instance, _, err := source.GetInstance(cName)
	if err != nil {
		return nil, fmt.Errorf("Failed to get instance info: %v", err)
	}

	// Optimization for the local copy case
	if destInfo.URL == sourceInfo.URL && destInfo.SocketPath == sourceInfo.SocketPath && (!r.IsClustered() || instance.Location == r.clusterTarget || r.HasExtension("cluster_internal_copy")) {
		// Project handling
		if destInfo.Project != sourceInfo.Project {
			if !r.HasExtension("container_copy_project") {
				return nil, fmt.Errorf("The server is missing the required \"container_copy_project\" API extension")
			}

			req.Source.Project = sourceInfo.Project
		}

		// Local copy source fields
		req.Source.Type = "copy"
		req.Source.Source = fmt.Sprintf("%s/%s", cName, sName)

		// Copy the instance
		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}

		rop := remoteOperation{
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

	// Source request
	sourceReq := api.InstanceSnapshotPost{
		Migration: true,
		Name:      args.Name,
	}
	if snapshot.Stateful && args.Live {
		sourceReq.Live = args.Live
	}

	// Push mode migration
	if args != nil && args.Mode == "push" {
		// Get target server connection information
		info, err := r.GetConnectionInfo()
		if err != nil {
			return nil, err
		}

		// Create the instance
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		op, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}
		opAPI := op.Get()

		targetSecrets := map[string]string{}
		for k, v := range opAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Prepare the source request
		target := api.InstancePostTarget{}
		target.Operation = opAPI.ID
		target.Websockets = targetSecrets
		target.Certificate = info.Certificate
		sourceReq.Target = &target

		return r.tryMigrateInstanceSnapshot(source, cName, sName, sourceReq, info.Addresses)
	}

	// Get source server connection information
	info, err := source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	op, err := source.MigrateInstanceSnapshot(cName, sName, sourceReq)
	if err != nil {
		return nil, err
	}
	opAPI := op.Get()

	sourceSecrets := map[string]string{}
	for k, v := range opAPI.Metadata {
		sourceSecrets[k] = v.(string)
	}

	// Relay mode migration
	if args != nil && args.Mode == "relay" {
		// Push copy source fields
		req.Source.Type = "migration"
		req.Source.Mode = "push"

		// Start the process
		targetOp, err := r.CreateInstance(req)
		if err != nil {
			return nil, err
		}
		targetOpAPI := targetOp.Get()

		// Extract the websockets
		targetSecrets := map[string]string{}
		for k, v := range targetOpAPI.Metadata {
			targetSecrets[k] = v.(string)
		}

		// Launch the relay
		err = r.proxyMigration(targetOp.(*operation), targetSecrets, source, op.(*operation), sourceSecrets)
		if err != nil {
			return nil, err
		}

		// Prepare a tracking operation
		rop := remoteOperation{
			targetOp: targetOp,
			chDone:   make(chan bool),
		}

		// Forward targetOp to remote op
		go func() {
			rop.err = rop.targetOp.Wait()
			close(rop.chDone)
		}()

		return &rop, nil
	}

	// Pull mode migration
	req.Source.Type = "migration"
	req.Source.Mode = "pull"
	req.Source.Operation = opAPI.ID
	req.Source.Websockets = sourceSecrets
	req.Source.Certificate = info.Certificate

	return r.tryCreateInstance(req, info.Addresses)
}

// RenameInstanceSnapshot requests that LXD renames the snapshot.
func (r *ProtocolLXD) RenameInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Sanity check
	if instance.Migration {
		return nil, fmt.Errorf("Can't ask for a migration through RenameInstanceSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

func (r *ProtocolLXD) tryMigrateInstanceSnapshot(source InstanceServer, instanceName string, name string, req api.InstanceSnapshotPost, urls []string) (RemoteOperation, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("The target server isn't listening on the network")
	}

	rop := remoteOperation{
		chDone: make(chan bool),
	}

	operation := req.Target.Operation

	// Forward targetOp to remote op
	go func() {
		success := false
		errors := map[string]error{}
		for _, serverURL := range urls {
			req.Target.Operation = fmt.Sprintf("%s/1.0/operations/%s", serverURL, url.PathEscape(operation))

			op, err := source.MigrateInstanceSnapshot(instanceName, name, req)
			if err != nil {
				errors[serverURL] = err
				continue
			}

			rop.targetOp = op

			for _, handler := range rop.handlers {
				rop.targetOp.AddHandler(handler)
			}

			err = rop.targetOp.Wait()
			if err != nil {
				errors[serverURL] = err
				continue
			}

			success = true
			break
		}

		if !success {
			rop.err = remoteOperationError("Failed instance migration", errors)
		}

		close(rop.chDone)
	}()

	return &rop, nil
}

// MigrateInstanceSnapshot requests that LXD prepares for a snapshot migration.
func (r *ProtocolLXD) MigrateInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Sanity check
	if !instance.Migration {
		return nil, fmt.Errorf("Can't ask for a rename through MigrateInstanceSnapshot")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), instance, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteInstanceSnapshot requests that LXD deletes the instance snapshot.
func (r *ProtocolLXD) DeleteInstanceSnapshot(instanceName string, name string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateInstanceSnapshot requests that LXD updates the instance snapshot.
func (r *ProtocolLXD) UpdateInstanceSnapshot(instanceName string, name string, instance api.InstanceSnapshotPut, ETag string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("snapshot_expiry") {
		return nil, fmt.Errorf("The server is missing the required \"snapshot_expiry\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s/%s/snapshots/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), instance, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetInstanceState returns a InstanceState entry for the provided instance name.
func (r *ProtocolLXD) GetInstanceState(name string) (*api.InstanceState, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	state := api.InstanceState{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s/state", path, url.PathEscape(name)), nil, "", &state)
	if err != nil {
		return nil, "", err
	}

	return &state, etag, nil
}

// UpdateInstanceState updates the instance to match the requested state.
func (r *ProtocolLXD) UpdateInstanceState(name string, state api.InstanceStatePut, ETag string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("PUT", fmt.Sprintf("%s/%s/state", path, url.PathEscape(name)), state, ETag)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetInstanceLogfiles returns a list of logfiles for the instance.
func (r *ProtocolLXD) GetInstanceLogfiles(name string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	urls := []string{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/logs", path, url.PathEscape(name)), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	logfiles := []string{}
	for _, uri := range logfiles {
		fields := strings.Split(uri, fmt.Sprintf("%s/%s/logs/", path, url.PathEscape(name)))
		logfiles = append(logfiles, fields[len(fields)-1])
	}

	return logfiles, nil
}

// GetInstanceLogfile returns the content of the requested logfile.
//
// Note that it's the caller's responsibility to close the returned ReadCloser
func (r *ProtocolLXD) GetInstanceLogfile(name string, filename string) (io.ReadCloser, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0%s/%s/logs/%s", r.httpHost, path, url.PathEscape(name), url.PathEscape(filename))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.do(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// DeleteInstanceLogfile deletes the requested logfile.
func (r *ProtocolLXD) DeleteInstanceLogfile(name string, filename string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/logs/%s", path, url.PathEscape(name), url.PathEscape(filename)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceMetadata returns instance metadata.
func (r *ProtocolLXD) GetInstanceMetadata(name string) (*api.ImageMetadata, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	if !r.HasExtension("container_edit_metadata") {
		return nil, "", fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	metadata := api.ImageMetadata{}

	url := fmt.Sprintf("%s/%s/metadata", path, url.PathEscape(name))
	etag, err := r.queryStruct("GET", url, nil, "", &metadata)
	if err != nil {
		return nil, "", err
	}

	return &metadata, etag, err
}

// SetInstanceMetadata sets the content of the instance metadata file.
func (r *ProtocolLXD) SetInstanceMetadata(name string, metadata api.ImageMetadata, ETag string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/%s/metadata", path, url.PathEscape(name))
	_, _, err = r.query("PUT", url, metadata, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceTemplateFiles returns the list of names of template files for a instance.
func (r *ProtocolLXD) GetInstanceTemplateFiles(instanceName string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_edit_metadata") {
		return nil, fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	templates := []string{}

	url := fmt.Sprintf("%s/%s/metadata/templates", path, url.PathEscape(instanceName))
	_, err = r.queryStruct("GET", url, nil, "", &templates)
	if err != nil {
		return nil, err
	}

	return templates, nil
}

// GetInstanceTemplateFile returns the content of a template file for a instance.
func (r *ProtocolLXD) GetInstanceTemplateFile(instanceName string, templateName string) (io.ReadCloser, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_edit_metadata") {
		return nil, fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/1.0%s/%s/metadata/templates?path=%s", r.httpHost, path, url.PathEscape(instanceName), url.QueryEscape(templateName))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.do(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// CreateInstanceTemplateFile creates an a template for a instance.
func (r *ProtocolLXD) CreateInstanceTemplateFile(instanceName string, templateName string, content io.ReadSeeker) error {
	return r.setInstanceTemplateFile(instanceName, templateName, content, "POST")
}

// UpdateInstanceTemplateFile updates the content for a instance template file.
func (r *ProtocolLXD) UpdateInstanceTemplateFile(instanceName string, templateName string, content io.ReadSeeker) error {
	return r.setInstanceTemplateFile(instanceName, templateName, content, "PUT")
}

func (r *ProtocolLXD) setInstanceTemplateFile(instanceName string, templateName string, content io.ReadSeeker, httpMethod string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}

	url := fmt.Sprintf("%s/1.0%s/%s/metadata/templates?path=%s", r.httpHost, path, url.PathEscape(instanceName), url.QueryEscape(templateName))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(httpMethod, url, content)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.do(req)
	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return err
		}
	}
	return err
}

// DeleteInstanceTemplateFile deletes a template file for a instance.
func (r *ProtocolLXD) DeleteInstanceTemplateFile(name string, templateName string) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("container_edit_metadata") {
		return fmt.Errorf("The server is missing the required \"container_edit_metadata\" API extension")
	}
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/metadata/templates?path=%s", path, url.PathEscape(name), url.QueryEscape(templateName)), nil, "")
	return err
}

// ConsoleInstance requests that LXD attaches to the console device of a instance.
func (r *ProtocolLXD) ConsoleInstance(instanceName string, console api.InstanceConsolePost, args *InstanceConsoleArgs) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("console") {
		return nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/console", path, url.PathEscape(instanceName)), console, "")
	if err != nil {
		return nil, err
	}
	opAPI := op.Get()

	if args == nil || args.Terminal == nil {
		return nil, fmt.Errorf("A terminal must be set")
	}

	if args.Control == nil {
		return nil, fmt.Errorf("A control channel must be set")
	}

	// Parse the fds
	fds := map[string]string{}

	value, ok := opAPI.Metadata["fds"]
	if ok {
		values := value.(map[string]interface{})
		for k, v := range values {
			fds[k] = v.(string)
		}
	}

	var controlConn *websocket.Conn
	// Call the control handler with a connection to the control socket
	if fds["control"] == "" {
		return nil, fmt.Errorf("Did not receive a file descriptor for the control channel")
	}

	controlConn, err = r.GetOperationWebsocket(opAPI.ID, fds["control"])
	if err != nil {
		return nil, err
	}

	go args.Control(controlConn)

	// Connect to the websocket
	conn, err := r.GetOperationWebsocket(opAPI.ID, fds["0"])
	if err != nil {
		return nil, err
	}

	// Detach from console.
	go func(consoleDisconnect <-chan bool) {
		<-consoleDisconnect
		msg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Detaching from console")
		// We don't care if this fails. This is just for convenience.
		controlConn.WriteMessage(websocket.CloseMessage, msg)
		controlConn.Close()
	}(args.ConsoleDisconnect)

	// And attach stdin and stdout to it
	go func() {
		shared.WebsocketSendStream(conn, args.Terminal, -1)
		<-shared.WebsocketRecvStream(args.Terminal, conn)
		conn.Close()
	}()

	return op, nil
}

// GetInstanceConsoleLog requests that LXD attaches to the console device of a instance.
//
// Note that it's the caller's responsibility to close the returned ReadCloser
func (r *ProtocolLXD) GetInstanceConsoleLog(instanceName string, args *InstanceConsoleLogArgs) (io.ReadCloser, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("console") {
		return nil, fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Prepare the HTTP request
	url := fmt.Sprintf("%s/1.0%s/%s/console", r.httpHost, path, url.PathEscape(instanceName))

	url, err = r.setQueryAttributes(url)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set the user agent
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Send the request
	resp, err := r.do(req)
	if err != nil {
		return nil, err
	}

	// Check the return value for a cleaner error
	if resp.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(resp)
		if err != nil {
			return nil, err
		}
	}

	return resp.Body, err
}

// DeleteInstanceConsoleLog deletes the requested instance's console log.
func (r *ProtocolLXD) DeleteInstanceConsoleLog(instanceName string, args *InstanceConsoleLogArgs) error {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	if !r.HasExtension("console") {
		return fmt.Errorf("The server is missing the required \"console\" API extension")
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("%s/%s/console", path, url.PathEscape(instanceName)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetInstanceBackupNames returns a list of backup names for the instance.
func (r *ProtocolLXD) GetInstanceBackupNames(instanceName string) ([]string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	urls := []string{}
	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/backups", path, url.PathEscape(instanceName)), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, uri := range urls {
		fields := strings.Split(uri, fmt.Sprintf("%s/%s/backups/", path, url.PathEscape(instanceName)))
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetInstanceBackups returns a list of backups for the instance.
func (r *ProtocolLXD) GetInstanceBackups(instanceName string) ([]api.InstanceBackup, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	backups := []api.InstanceBackup{}

	_, err = r.queryStruct("GET", fmt.Sprintf("%s/%s/backups?recursion=1", path, url.PathEscape(instanceName)), nil, "", &backups)
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// GetInstanceBackup returns a Backup struct for the provided instance and backup names.
func (r *ProtocolLXD) GetInstanceBackup(instanceName string, name string) (*api.InstanceBackup, string, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, "", err
	}

	if !r.HasExtension("container_backup") {
		return nil, "", fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Fetch the raw value
	backup := api.InstanceBackup{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("%s/%s/backups/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "", &backup)
	if err != nil {
		return nil, "", err
	}

	return &backup, etag, nil
}

// CreateInstanceBackup requests that LXD creates a new backup for the instance.
func (r *ProtocolLXD) CreateInstanceBackup(instanceName string, backup api.InstanceBackupsPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/backups", path, url.PathEscape(instanceName)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// RenameInstanceBackup requests that LXD renames the backup.
func (r *ProtocolLXD) RenameInstanceBackup(instanceName string, name string, backup api.InstanceBackupPost) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("%s/%s/backups/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), backup, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteInstanceBackup requests that LXD deletes the instance backup.
func (r *ProtocolLXD) DeleteInstanceBackup(instanceName string, name string) (Operation, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("DELETE", fmt.Sprintf("%s/%s/backups/%s", path, url.PathEscape(instanceName), url.PathEscape(name)), nil, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetInstanceBackupFile requests the instance backup content.
func (r *ProtocolLXD) GetInstanceBackupFile(instanceName string, name string, req *BackupFileRequest) (*BackupFileResponse, error) {
	path, _, err := r.instanceTypeToPath(api.InstanceTypeAny)
	if err != nil {
		return nil, err
	}

	if !r.HasExtension("container_backup") {
		return nil, fmt.Errorf("The server is missing the required \"container_backup\" API extension")
	}

	// Build the URL
	uri := fmt.Sprintf("%s/1.0%s/%s/backups/%s/export", r.httpHost, path, url.PathEscape(instanceName), url.PathEscape(name))
	if r.project != "" {
		uri += fmt.Sprintf("?project=%s", url.QueryEscape(r.project))
	}

	// Prepare the download request
	request, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return nil, err
	}

	if r.httpUserAgent != "" {
		request.Header.Set("User-Agent", r.httpUserAgent)
	}

	// Start the request
	response, doneCh, err := cancel.CancelableDownload(req.Canceler, r.http, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	defer close(doneCh)

	if response.StatusCode != http.StatusOK {
		_, _, err := lxdParseResponse(response)
		if err != nil {
			return nil, err
		}
	}

	// Handle the data
	body := response.Body
	if req.ProgressHandler != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: response.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: response.ContentLength,
				Handler: func(percent int64, speed int64) {
					req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
				},
			},
		}
	}

	size, err := io.Copy(req.BackupFile, body)
	if err != nil {
		return nil, err
	}

	resp := BackupFileResponse{}
	resp.Size = size

	return &resp, nil
}
