(access-ui)=
# How to access the LXD web UI

```{note}
The LXD web UI is available as part of the LXD snap.

See the [LXD-UI GitHub repository](https://github.com/canonical/lxd-ui) for the source code.
```

```{figure} /images/ui_console.png
:width: 100%
:alt: Graphical console of an instance in the LXD web UI

Graphical console of an instance in the LXD web UI
```

```{youtube} https://www.youtube.com/watch?v=wqEH_d8LC1k
```

The LXD web UI provides you with a graphical interface to manage your LXD server and instances.
It does not provide full functionality yet, but it is constantly evolving, already covering many of the features of the LXD command-line client.

Complete the following steps to access the LXD web UI:

1. Make sure that your LXD server is {ref}`exposed to the network <server-expose>`.
   You can expose the server during {ref}`initialization <initialize>`, or afterwards by setting the {config:option}`server-core:core.https_address` server configuration option.

<!-- Include start access UI -->

2. Access the UI in your browser by entering the server address (for example, [`https://127.0.0.1:8443`](https://127.0.0.1:8443) for a local server, or an address like `https://192.0.2.10:8443` for a server running on `192.0.2.10`).

   If you have not set up a secure {ref}`authentication-server-certificate`, LXD uses a self-signed certificate, which will cause a security warning in your browser.
   Use your browser's mechanism to continue despite the security warning.

   ```{figure} /images/ui_security_warning.png
   :width: 80%
   :alt: Example for a security warning in Chrome
   ```

1. Set up the certificates that are required for the UI client to authenticate with the LXD server by following the steps presented in the UI.

   You have two options, depending on whether you already have a client certificate selected in your browser:

   - If you don't have a certificate yet, click {guilabel}`Create a new certificate` to get instructions for creating a set of certificates, adding the public key to the server's trust store, and adding the private key to your browser.

     ```{figure} /images/ui_set_up_certificates.png
     :width: 100%
     :alt: Instructions for setting up certificates for the UI
     ```

   - If you already have a client certificate in your browser, select "use an existing certificate" to authorize the certificate with the server and re-use it.

     ```{figure} /images/ui_set_up_existing_cert.png
     :width: 100%
     :alt: Instructions for re-using an existing certificate for the UI
     ```

   See {ref}`authentication` for more information.

<!-- Include end access UI -->

After setting up the certificates, you can start creating instances, editing profiles, or configuring your server.

## Enable or disable the UI

The {ref}`snap configuration option <howto-snap-configure>` `lxd ui.enable` controls whether the UI is enabled for LXD.

Starting with LXD 5.21, the UI is enabled by default.
If you want to disable it, set the option to `false`:

    sudo snap set lxd ui.enable=false
    sudo systemctl reload snap.lxd.daemon

To enable it again, or to enable it for older LXD versions (that include the UI), set the option to `true`:

    sudo snap set lxd ui.enable=true
    sudo systemctl reload snap.lxd.daemon
