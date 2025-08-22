(dev-lxd)=
# Communication between instance and host

```{youtube} https://www.youtube.com/watch?v=xZSnqqWykmo
```

The DevLXD API allows for limited communication between guest instances and the host.

The API is available inside each LXD guest as a Unix socket at `/dev/lxd/sock`, using JSON over plain HTTP.
Multiple concurrent connections are allowed.

```{note}
{config:option}`instance-security:security.devlxd` must be set to `true` (which is the default) for an instance to allow access to the socket.

Additionally, for virtual machines, the LXD agent must be present and running for the socket to be available.
```

(dev-lxd-implementation)=
## Implementation details

(dev-lxd-implementation-containers)=
### Containers

LXD on the host binds `/var/lib/lxd/devlxd/sock` and listens for connections.
This single socket is exposed into every container started by LXD at `/dev/lxd/sock`.

```{note}
The alternative to using a single socket is to create a socket for every container.
This approach was discarded to avoid issues with file descriptor limits for hosts with thousands of containers.
```

(dev-lxd-implementation-vms)=
### Virtual machines

LXD on the host starts a HTTPS {abbr}`Vsock (Virtual Socket)` server.
The LXD agent on the virtual machine communicates securely with the Vsock server using a certificate mounted in the VM's configuration drive.
The LXD agent creates the socket at `/dev/lxd/sock` and proxies requests to the Vsock server.

(devlxd-authentication)=
## Authentication

Queries on `/dev/lxd/sock` only return information related to the requesting instance.

For containers, LXD inspects user credentials associated with the connection and matches them with a running instance.

For virtual machines, LXD extracts the virtual socket ID from the remote address of the caller (the LXD agent), and matches it with a virtual machine.

(devlxd-authentication-bearer)=
### Bearer tokens

Processes within guest instances can now authenticate over the DevLXD socket using a bearer token.
To do this, set an `Authorization: Bearer {token}` header on requests to the socket.

Bearer tokens can be obtained by creating a `DevLXD token bearer` identity in the identities API and issuing a token for it.
For more information, see {ref}`devlxd-authenticate`.

(devlxd-api-spec)=
## REST-API

<link rel="stylesheet" type="text/css" href="../_static/swagger-ui/swagger-ui.css" ></link>
<link rel="stylesheet" type="text/css" href="../_static/swagger-override.css" ></link>
<div id="swagger-ui"></div>

<script src="../_static/swagger-ui/swagger-ui-bundle.js" charset="UTF-8"> </script>
<script src="../_static/swagger-ui/swagger-ui-standalone-preset.js" charset="UTF-8"> </script>
<script>
window.onload = function() {
  // Begin Swagger UI call region
  const ui = SwaggerUIBundle({
    url: window.location.pathname + "../devlxd-api.yaml",
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [],
    validatorUrl: "none",
    defaultModelsExpandDepth: -1,
    supportedSubmitMethods: []
  })
  // End Swagger UI call region

  window.ui = ui
}
</script>
