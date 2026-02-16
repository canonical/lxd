(access-ui)=
# How to access the LXD web UI

```{note}
The LXD web UI is available as part of the LXD snap.

See the [LXD-UI GitHub repository](https://github.com/canonical/lxd-ui) for the source code.
```

```{figure} /images/UI/console.png
:width: 100%
:alt: Graphical console of an instance in the LXD web UI

Graphical console of an instance in the LXD web UI
```

```{youtube} https://www.youtube.com/watch?v=wqEH_d8LC1k
:title: Early look at the LXD web UI
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

Access to the UI can be obtained in two ways:

- Temporary access via a UI access link (bearer token, valid for 1 day).
- Permanent access using a browser certificate and trust token.

(access-ui-setup-temporary-access-link)=
### UI access using temporary link

The `lxd init` command guides you through the LXD initialization process.
When the server address is configured during the initialization process, LXD offers an option to generate a temporary UI access link. If you agree with that option, a temporary LXD UI access URL that is valid for 24 hours is printed at the end of the process, as shown below.

```bash
...
Would you like the LXD server to be available over the network? (yes/no) [default=no]: yes
Address to bind LXD to (not including port) [default=all]:
Port to bind LXD to [default=8443]:
Would you like to create a temporary LXD UI access link? (yes/no) [default=no]: yes
...
UI temporary identity (type: Client token bearer): ui-admin-temporary
UI temporary access link (expires: 2026-01-17 16:36): https://127.0.0.1:8443/?token=<bearer_token>
```

Open this URL in your browser to immediately access the UI as an admin.
This method is intended for initial access and setup only. After logging in, configure the permanent authentication (mTLS or OIDC) for continued access.

To obtain a new temporary UI access link, run `lxd init` again.
For convenience, the `--ui-temporary-access-link` flag can be used to non-interactively generate a new one.

```bash
lxd init --ui-temporary-access-link
```

(access-ui-setup-certificate)=
### Permanent UI access using browser certificate

Permanent access to the UI requires both a browser certificate and a trust token.

<!-- Include start access UI -->

If you have not set up a secure {ref}`authentication-server-certificate`, LXD uses a self-signed certificate, which will cause a security warning in your browser. Use your browser's mechanism to continue this time despite the security warning.

For example, in Chrome, click **Advanced**, then follow the link to **Proceed** at the bottom as shown below:

```{figure} /images/ui_security_warning.png
:width: 80%
:alt: Example for a security warning in Chrome
```

In Firefox, click **Advanced**, then follow the link to **Accept the risk and continue**.

#### Set up the browser certificate

Follow the instructions in the LXD UI browser page to install and select the browser certificate, also called a client certificate.

If you have previously installed a certificate for the LXD UI, your browser will offer you the option to use it. Confirm that the installed certificate's issuer is listed in the LXD UI, then select it.

After you have selected your certificate, follow the LXD UI's on-page instructions to set up the trust token.

Finally, click {guilabel}`Connect` in the UI to complete gaining access. You should then see the {guilabel}`Instances` page.

<!-- Include end access UI -->

Now you can start creating instances, editing profiles, or configuring your server.

For detailed information about the authentication process, see: {ref}`authentication`.
