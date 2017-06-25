// Package lxd implements a client for the LXD API
//
// Overview
//
// This package lets you connect to LXD daemons or SimpleStream image
// servers over a Unix socket or HTTPs. You can then interact with those
// remote servers, creating containers, images, moving them around, ...
//
// Example - container creation
//
// This creates a container on a local LXD daemon and then starts it.
//
//  // Connect to LXD over the Unix socket
//  c, err := lxd.ConnectLXDUnix("", nil)
//  if err != nil {
//    return err
//  }
//
//  // Container creation request
//  req := api.ContainersPost{
//    Name: "my-container",
//    Source: api.ContainerSource{
//      Type:  "image",
//      Alias: "my-image",
//    },
//  }
//
//  // Get LXD to create the container (background operation)
//  op, err := c.CreateContainer(req)
//  if err != nil {
//    return err
//  }
//
//  // Wait for the operation to complete
//  err = op.Wait()
//  if err != nil {
//    return err
//  }
//
//  // Get LXD to start the container (background operation)
//  reqState := api.ContainerStatePut{
//    Action: "start",
//    Timeout: -1,
//  }
//
//  op, err = c.UpdateContainerState(name, reqState, "")
//  if err != nil {
//    return err
//  }
//
//  // Wait for the operation to complete
//  err = op.Wait()
//  if err != nil {
//    return err
//  }
//
// Example - command execution
//
// This executes an interactive bash terminal
//
//  // Connect to LXD over the Unix socket
//  c, err := lxd.ConnectLXDUnix("", nil)
//  if err != nil {
//    return err
//  }
//
//  // Setup the exec request
//  req := api.ContainerExecPost{
//    Command: []string{"bash"},
//    WaitForWS: true,
//    Interactive: true,
//    Width: 80,
//    Height: 15,
//  }
//
//  // Setup the exec arguments (fds)
//  args := lxd.ContainerExecArgs{
//    Stdin: os.Stdin,
//    Stdout: os.Stdout,
//    Stderr: os.Stderr,
//  }
//
//  // Setup the terminal (set to raw mode)
//  if req.Interactive {
//    cfd := int(syscall.Stdin)
//    oldttystate, err := termios.MakeRaw(cfd)
//    if err != nil {
//      return err
//    }
//
//    defer termios.Restore(cfd, oldttystate)
//  }
//
//  // Get the current state
//  op, err := c.ExecContainer("c1", req, &args)
//  if err != nil {
//    return err
//  }
//
//  // Wait for it to complete
//  err = op.Wait()
//  if err != nil {
//    return err
//  }
//
// Example - image copy
//
// This copies an image from a simplestreams server to a local LXD daemon
//
//  // Connect to LXD over the Unix socket
//  c, err := lxd.ConnectLXDUnix("", nil)
//  if err != nil {
//    return err
//  }
//
//  // Connect to the remote SimpleStreams server
//  d, err = lxd.ConnectSimpleStreams("https://images.linuxcontainers.org", nil)
//  if err != nil {
//    return err
//  }
//
//  // Resolve the alias
//  alias, _, err := d.GetImageAlias("centos/7")
//  if err != nil {
//    return err
//  }
//
//  // Get the image information
//  image, _, err := d.GetImage(alias.Target)
//  if err != nil {
//    return err
//  }
//
//  // Ask LXD to copy the image from the remote server
//  op, err := d.CopyImage(*image, c, nil)
//  if err != nil {
//    return err
//  }
//
//  // And wait for it to finish
//  err = op.Wait()
//  if err != nil {
//    return err
//  }
package lxd
