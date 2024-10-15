# Exporting a LXD cluster to TF and synchronize a target cluster from a source cluster


```
cluster-sync \
    --src-cluster-cert <.crt filepath> \
    --src-cluster-key <.key filepath> \
    --src-cluster-remote      <NAME> \
    --src-cluster-remote-addr <HTTPS_ADDR> \
    --dst-cluster-cert <.crt filepath> \
    --dst-cluster-key <.key filepath> \
    --dst-cluster-remote      <NAME> \
    --dst-cluster-remote-addr <HTTPS_ADDR> \
    [--bootstrap] \
    [--tf-dir <TF_DIR>] \
    [--out-hcl-filename <NAME>] \
    [--auto-plan] \
    [--auto-apply] \
    [--align-server-configurations] \
    [--verbose]
```

## Scenarios

| Test Scenario             | State                                           | Link                |
|---------------------------|-------------------------------------------------|-------------------|
| **Simple LXD cluster export+bootstrap as TF definition**    | ✔  | [![asciicast](https://asciinema.org/a/riGfHDJGWEIAOfxI39H9nx89Y.svg)](https://asciinema.org/a/riGfHDJGWEIAOfxI39H9nx89Y)
| **Create a SRC cluster clone into a DST cluster**    | NOT TESTED  | 
| **Create a SRC cluster clone into a DST cluster (with server alignments)**    | NOT TESTED  | 
| **Create a SRC cluster clone into a DST cluster (with server alignments + auto plan + auto apply)**    | NOT TESTED  | 
| **Keep a DST cluster in sync with an SRC cluster**    | NOT TESTED  | 
| **Keep a DST cluster in sync with an SRC cluster (with auto plan + auto apply)**    | NOT TESTED  | 

## TODOs

* [FUTURE] Add storage (pools and volumes), profile and instance entities.
* [FUTURE] Storage pools have a `source`: how to ensure that a storage pool to be created in the dst cluster have the same `source` so that we can potentially error out before `tf apply` if the dst source does not exist?


## For me only

* How to talk to LXD server inside `micro1` on remote computer?

```bash
ssh -L 8444:10.237.134.244:8443 gab@192.168.1.29
```

* Then add client.crt and client.key to this LXD server inside remote instance `micro1`
* Add the remote: `lxc remote add dst-cluster https://127.0.0.1:8444`