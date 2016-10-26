Here are different ways to help troubleshooting `lxc` and `lxd` code.

#### lxc --debug

Adding `--debug` flag to any client command will give extra information
about internals. If there is no useful info, it can be added with the
logging call:

    shared.LogDebugf("Hello: %s", "Debug")
    
#### lxc monitor

This command will monitor messages as they appear on remote server.

#### lxd --debug

Shutting down `lxd` server and running it in foreground with `--debug`
flag will bring a lot of (hopefully) useful info:

    systemctl stop lxd lxd.socket
    lxd --debug

### Testing REST API through browser

There are browser plugins that provide convenient interface to create,
modify and replay web requests. Usually they won't pass through LXD
authorization level. To make that possible, find certificate generated
for `lxc` and import it into browser.
