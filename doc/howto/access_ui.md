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

(access-ui-expose)=
## Expose the server to the network

Make sure that your LXD server is {ref}`exposed to the network <server-expose>`.
   You can expose the server during {ref}`initialization <initialize>`, or afterwards by setting the {config:option}`server-core:core.https_address` server configuration option.

(access-ui-browser)=
## Access the UI in your browser

Access the UI in your browser by entering the server address (for example, [`https://127.0.0.1:8443`](https://127.0.0.1:8443) for a local server, or an address like `https://192.0.2.10:8443` for a server running on `192.0.2.10`).

If you have already set up access to the UI, you will see the {guilabel}`Instances` page. For setup instructions, continue below.

(access-ui-setup)=
## Set up access

<!-- Include start access UI -->

Access to the UI requires both a browser certificate and a trust token.

### Set up the browser certificate

If you already have a client certificate for the LXD UI installed in your browser, a banner displays titled "Client certificate already present." Click "Skip to step 2" to [set up the trust token](#set-up-the-trust-token).

```{figure} /images/UI/certificate-skip.png
:width: 100%
:alt: Banner that indicates a client certificate is already present
```

If you don't have a certificate yet, choose the tab in the UI that corresponds to your environment. Follow the instructions to generate and import a `.pfx` certificate. If prompted by your browser to select a certificate, select the one you imported.

The screenshot below shows the tab for Chrome on Linux. Make sure to select the correct tab for your environment.

```{figure} /images/UI/certificate-create.png
:width: 100%
:alt: Instructions for setting up certificates for the UI
```

When finished, click the {guilabel}`Trust token` step in the left-hand menu.

### Set up the trust token

Follow the instructions to generate a trust token and provide it to your browser.

Example:

```{figure} /images/UI/token-create.png
:width: 100%
:alt: Example for generating a LXD UI trust token and providing it to the browser
```

Click {guilabel}`Connect` to complete the login process.

<!-- Include end access UI -->

Now you can start creating instances, editing profiles, or configuring your server.

For detailed information about the authentication process, see: {ref}`authentication`.

(access-ui-enable)=
## Enable or disable the UI

The {ref}`snap configuration option <howto-snap-configure>` `lxd ui.enable` controls whether the UI is enabled for LXD.

Starting with LXD 5.21, the UI is enabled by default.
If you want to disable it, set the option to `false`:

    sudo snap set lxd ui.enable=false
    sudo systemctl reload snap.lxd.daemon

To enable it again, or to enable it for older LXD versions (that include the UI), set the option to `true`:

    sudo snap set lxd ui.enable=true
    sudo systemctl reload snap.lxd.daemon
