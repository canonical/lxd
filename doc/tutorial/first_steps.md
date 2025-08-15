(first-steps)=
(tutorial-first-steps)=
# First steps with LXD

This tutorial guides you through your first steps with LXD. You'll begin by installing and initializing LXD. Then you'll use its CLI or graphical web UI to work with instances, including both containers and virtual machines. You'll learn how to create and configure instances, create snapshots, and more.

````{only} integrated

```{admonition} For MicroCloud users
:class: note
The MicroCloud setup process installs and initializes LXD. Thus, you can skip those sections on this page.
```

````

(tutorial-requirements)=
## Requirements

- At least 20 GiB free disk space
- A Linux distribution installed

(tutorial-install)=
## Install LXD using snap

This section of the tutorial assumes that you have the `snap` packaging system available on your system, which is the recommended way to use LXD.

If you use a Linux distribution that does not support `snap`, see {ref}`installing-other` and skip to the next section of this tutorial.

**LXD snap installation requirements**

- The LXD snap must be [available for your Linux distribution](https://snapcraft.io/lxd#distros).
- The [`snapd` daemon](https://snapcraft.io/docs/installing-snapd) must be installed.

The `snapd` daemon that manages snap packages comes pre-installed on many distributions by default. To confirm whether it is available on your system, run:

```bash
snap version
```

If you see an error message indicating that `snap` is not installed, visit the [Snap installation documentation](https://snapcraft.io/docs/installing-snapd) and follow the instructions there to install it.

Once you have confirmed that `snap` is available on your system, use it to install LXD:

```bash
sudo snap install lxd
```

### If the LXD snap is already installed

This tutorial is designed for LXD version {{current_lts_track}} and higher. If you see an error message that the LXD snap is already installed, run the following command to find the channel the snap is tracking:

```bash
snap list lxd
```

The `Tracking` column lists the installed {ref}`snap channel <ref-snap-channels>`. If the number shown is {{current_lts_track}} or higher, run the following command to update the snap to the most recent release in its channel:

```bash
sudo snap refresh lxd
```

Otherwise, if the number shown is lower, an older version is installed. In this case, upgrade to the {{current_lts_track}}/stable channel by following the instructions in this guide: {ref}`howto-snap-change`.

(tutorial-adduser)=
## Add the current user to the `lxd` group

````{admonition} Important security notice
:class: important
% Include content from ../../README.md
```{include} ../../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
For more information, see {ref}`security-daemon-access`.
````

Installing LXD through its snap should automatically create an `lxd` group on your system. The user you are logged in as must be in this group to interact with LXD.

Check to see if the current user is already in that group:

```bash
getent group lxd | grep "$USER"
```

If this command returns a result, you're set up correctly and can continue with the next section.

If there is no result, enter the following commands. The first command adds your user to the `lxd` group.
The second command starts a new shell where the group membership takes effect immediately.

```bash
sudo usermod -aG lxd "$USER"
newgrp lxd
```

(tutorial-initialize)=
## Initialize LXD

Next, initialize LXD using a minimal setup with default options.

Run:

```bash
lxd init --minimal
```

If this command results in an error, your group membership might not have taken effect. In this case, close and re-open your terminal, then try again.

The `lxd init` command can be run again later to update the options. Once you have learned more about LXD, you might want to tune the {ref}`initialization options <initialize>` according to your own preferences, or learn how to {ref}`use a preseed file <initialize-preseed>` to initialize LXD. For now, the minimal configuration is sufficient.

(tutorial-types)=
## Containers and virtual machines

LXD supports two instance types: containers and virtual machines. LXD containers are faster and more lightweight than virtual machines, but share the host system's OS kernel. Virtual machines use their own kernel. For more information about these instance types, see {ref}`containers-and-vms`.

(tutorial-virtualization)=
### Confirm virtualization support

For LXD virtual machines, your host system must be capable of KVM virtualization. To test for this, run:

```bash
lxc info | grep -FA2 'instance_types'
```

If your host system is capable of KVM virtualization, you should see `virtual-machine` in the list of `instance_types`:

```{terminal}
:input: lxc info | grep -FA2 'instance_types'

instance_types:
       - container
       - virtual-machine
```

If `virtual-machine` fails to appear in the output, this indicates that the host system is not capable of virtualization. In this case, LXD can only be used for containers. You can proceed with this tutorial to learn about using containers, but disregard the steps that refer to virtual machines.

(tutorial-enable-ui)=
## Optional: Enable the LXD UI

While the installation and initialization steps must be performed via the command line interface, a graphical interface (the LXD UI) is available for use after these setup steps. The LXD UI is accessed through your web browser.

If you prefer to use the LXD UI, expand and follow the steps below.

````{dropdown} View steps to enable the LXD UI

1. By default, LXD is exposed through a Unix socket only and is not accessible over HTTPS. To access and manage LXD through a web browser using HTTPS, we must set the {config:option}`server-core:core.https_address` server configuration option. Run:

```bash
lxc config set core.https_address :8443
```

```{include} ../howto/access_ui.md
:start-after: <!-- Include start access UI -->
:end-before: <!-- Include end access UI -->
```
````

Most of the following sections include sets of tabs. When the `UI` tab is available, use the instructions in that tab.

(tutorial-create-instances)=
## Create instances

LXD uses images to create instances from either {ref}`local or remote image servers <about-images>`. We will fetch our container images from the remote [`ubuntu:`](https://cloud-images.ubuntu.com/releases/) server, which hosts official Ubuntu images.

(tutorial-create-containers)=
### Create and start containers

`````{tabs}
````{group-tab} CLI

For managing instances, we use the [`lxc` command instead of `lxd`](lxd-lxc).

The `lxc launch` command creates an instance, then immediately starts it. By default, it creates a container instead of a virtual machine. Use this command to launch a container named `first`, based on the Ubuntu 24.04 LTS image:

```bash
lxc launch ubuntu:24.04 first
```

This downloads and unpacks the image, then uses it to create and start a container. Since this command does not specify a remote server, the default [`ubuntu:`](https://cloud-images.ubuntu.com/releases/) server is used. Once downloaded, this image is cached temporarily in the local image server.

We can also create an instance without starting it, using the `lxc init` command. Note that this differs from the `lxd init` command you used to initialize LXD.

Create a container called `second` but do not start it, using the same image as the first:

```bash
lxc init ubuntu:24.04 second
```

Since the image is now cached locally, this container is created much more quickly than the first.

To confirm that the containers have been created, run:

```bash
lxc list
```

You should see both containers you created in the output, with the `first` container in a `RUNNING` state and the `second` container in a `STOPPED` state.

````
% End group-tab CLI

````{group-tab} UI

To create a container, select {guilabel}`Instances` from the main navigation, then click {guilabel}`Create instance`.

In the form that opens, name this instance `first`. Click {guilabel}`Browse images` and select the `Ubuntu 24.04 LTS` image. Note that the {guilabel}`Source` for this image is `Ubuntu`, which is the remote [`ubuntu:`](https://cloud-images.ubuntu.com/releases/) server.

Launch the container by clicking the {guilabel}`Create and start` button.

This downloads and unpacks the image, then uses it to create and start a container. The image is cached temporarily in the local image server.

Create another container named `second`, using the same image. This time, click {guilabel}`Create` instead of {guilabel}`Create and start`. This container will be created but not started.

Since the image is now cached locally, this container is created much more quickly than the first.

````
`````

(tutorial-create-vm)=
## Create and start a VM

Next, let's launch a VM using the Ubuntu 24.04 LTS image.

Although we will use the same image name as we used when creating a container, LXD will download a variant of the image built specifically for VMs. This image is not yet cached, and it is larger than the container VM, so it will take longer to download.

`````{tabs}
````{group-tab} CLI

We will use the same `lxc launch` command, this time to create an instance named `ubuntu-vm`. To create it as a VM instead of a container, we must add the `--vm` flag. Run:

```bash
lxc launch ubuntu:24.04 ubuntu-vm --vm
```

````
% End group-tab CLI

````{group-tab} UI

Open the form to create an instance, and set its name to `ubuntu-vm`. Browse for an image. Do not select the the cached Ubuntu 24.04 image at the top, which has a {guilabel}`Type` set to `container`. Instead, select the Ubuntu 24.04 LTS image with the {guilabel}`Type` of `all`, which can be used for VMs.

After you select this image and return to the main creation form, set the {guilabel}`Instance type` to `VM`. Create and start the VM.

```{figure} /images/tutorial/create_vm.png
:width: 100%
:alt: Create an Ubuntu 24.04 LTS VM

```
````
`````

(tutorial-create-vm-desktop)=
## Configure, create, and start a desktop VM

A desktop Ubuntu VM is available from the remote [`images:`](https://images.lxd.canonical.com/) server. This server is provided by Canonical for unofficial images of not only Ubuntu variants but other Linux distributions, for testing and development purposes.

The {config:option}`instance-resource-limits:limits.memory` option defaults to 1 GiB for VMs. For the desktop VM to run smoothly, we must allocate a higher memory limit.

`````{tabs}
````{group-tab} CLI

You can configure instance options during {ref}`creation <instances-create>` or {ref}`afterward <instances-configure>`. We will configure the desktop VM during creation, using the `--config` flag to set {config:option}`instance-resource-limits:limits.memory` to `4GiB`.

Run:

```bash
lxc launch images:ubuntu/24.04/desktop ubuntu-desktop --vm --config limits.memory=4GiB
```

```{tip}
This is a large image and can take a while to download. If you like, you can open a separate terminal and continue with the next sections of the tutorial while you wait for the download to finish.
```

Once the VM has launched, confirm that its memory limit is set to `4 GiB`:

```bash
lxc config get ubuntu-desktop limits.memory
```
````

% End group-tab CLI

````{group-tab} UI
Open the form to create a new instance. Name it `ubuntu-desktop`.

Browse for its image, and filter by variant `desktop` to find the Ubuntu 24.04 LTS desktop image. Note that its {guilabel}`Source` is `LXD Images`, meaning that it uses the remote [`images:`](https://images.lxd.canonical.com/) server. Select this image.

Next, go to {guilabel}`Advanced` > {guilabel}`Resource limits` and set the {guilabel}`Memory limit` to 4 GiB.

Finally, click {guilabel}`Create and start` to start the VM.

````
`````

(tutorial-inspect)=
## Inspect instances

`````{tabs}
````{group-tab} CLI

List all the instances that you created:

```bash
lxc list
```

The output tells you the name, state, IP addresses, instance type, and number of snapshots for each instance.

You can retrieve further information about each instance with `lxc info`, including its architecture, process ID, usage data, and more. Run:

```bash
lxc info first
```

````
% End group-tab CLI

````{group-tab} UI

View the list of instances that you created:

```{figure} /images/tutorial/instances.png
:width: 100%
:alt: List of instances
```

This list tells you the name, instance type, description, IPv4 address, and status for each instance. When you hover over an instance row, icons appear to start, restart, freeze (pause), or stop the instance.

Click one of the rows (but not on the instance name) to view the instance summary panel:

```{figure} /images/tutorial/instance_summary.png
:width: 100%
:alt: Information about an instance in the instance summary
```

This panel provides more information, including the instance's architecture and process ID.

In either the list of instances or the instance summary panel, click the name of the instance to view its detail page. The {guilabel}`Overview` tab displays general information about the instance, including its architecture, process ID, creation date, and usage data. You can also view the instance's network, devices, and profiles here.

Click the {guilabel}`Configuration` tab. From here, you can both view and edit the instance configuration details. You can also click the {guilabel}`YAML Configuration` toggle at the bottom of this tab to view and edit the full YAML representation of the instance configuration:

```{figure} /images/tutorial/yaml_configuration.png
:width: 100%
:alt: YAML configuration of an instance
```

The {guilabel}`Console` tab is mainly useful for viewing information from the startup process of an instance, and for viewing the graphic console of a desktop VM. Open this tab for the `ubuntu-desktop` VM to see that you can access the graphic console.

You can view and download log files from the {guilabel}`Logs` tab. Running instances log only limited information by default. More log files are added if an instance ends up in an error state.

We will explore the {guilabel}`Terminal` and {guilabel}`Snapshots` tabs later in this tutorial. For now, click the word {guilabel}`Instances` at the top of this page to return to the list of instances.

````
`````

(tutorial-start)=
### Start a stopped instance

`````{tabs}
````{group-tab} CLI

When you ran `lxc list`, you saw that the `second` container's state is `STOPPED`, because we used `lxc init` to create the container instead of `lxc launch`.

Start the `second` container:

```bash
lxc start second
```

Run `lxc list` again to confirm that it is now in a `RUNNING` state.

````
% End group-tab CLI

````{group-tab} UI

In the list of instances, you should see that the `second` container's state is `Stopped`, because you created the container but did not start it.

Start the `second` container by clicking the {guilabel}`Start` button (▷) that appears when you hover over its row.

````
`````

See {ref}`instances-create` and {ref}`instances-manage` for more information.

(tutorial-configure)=
## Configure instances

Each instance created inherits a default set of configuration options. You can customize these options for each instance. See {ref}`instance-options` for a list of available options.

Earlier, we set the {config:option}`instance-resource-limits:limits.memory` option for the `ubuntu-desktop` VM during its creation. We can also update an instance's configuration after creation.

As an example, let's reduce the `second` container's resource limits. Follow the instructions below to update its {config:option}`instance-resource-limits:limits.cpu` to `1`, and its {config:option}`instance-resource-limits:limits.memory` to `192MiB`.

`````{tabs}
````{group-tab} CLI

Run:

```bash
lxc config set second limits.cpu=1 limits.memory=192MiB
```

To confirm that the options have been set, use the `lxc config get` command for each option:

```bash
lxc config get second limits.cpu
lxc config get second limits.memory
```

You can also use the `lxc config show` command to view values for all the options. Run:

```bash
lxc config show second
```

````
% End group-tab CLI

````{group-tab} UI
Go to the detail page for the `second` container, then its {guilabel}`Configuration` tab.

From the {guilabel}`Resource limits` section, override the `Exposed CPU limit` to a `number` of `1`.

Override the `Memory limit` to an `absolute` value of 192. Change the dropdown value from `GiB` to `MiB`.

Save the updated configuration, then confirm that you see the updated values reflected in the {guilabel}`Configuration` tab.

````
`````

(tutorial-shell)=
## Open an interactive shell into instances

Thus far, we have only acted upon instances from outside of them, from the host system. It's time to see what we can do inside an instance.

First, let's run a couple of standard Linux commands on your host system. The first command below displays memory information in megabytes, and the second displays the number of available CPUs.

In a terminal, run:

```bash
free -m
nproc
```

Take note of the outputs. We will compare them to the outputs from the same commands run within your instances.

`````{tabs}
````{group-tab} CLI

Use `lxc shell` to open an interactive shell into the `first` container:

```bash
lxc shell first
```

Notice that your command prompt has changed. You are now logged in as `root` inside the `first` instance.

In this shell session, run the same commands as you did on the host:

```bash
free -m
nproc
```

Note that the total memory returned by `free -m` and the value returned by `nproc` are identical for the host system and the `first` container. This is because by default, containers inherit the resources from their host environment.

Next, exit the `first` container:

```bash
exit
```

Enter an interactive shell into the `second` container:

```bash
lxc shell second
```

Then in the `second` container, run the same commands:

```bash
free -m
nproc
```

For the `second` container, notice that only `192 MiB` total memory and `1` CPU is available. These are the options that we configured for this container earlier.

You can try other commands to interact with your instance. For example, enter the following command to display information about the operating system:

```bash
cat /etc/*release
```

Or have some fun:

```bash
apt update
apt install fortune -y
/usr/games/fortune
```

When you're done, exit the shell:

```bash
exit
```

Your command prompt should return to that of the host system. From here, try out one other way to run commands inside an instance: the `lxc exec` command. This command is used to execute a single command inside an instance from the host system, without opening a shell. Run:

```bash
lxc exec second -- free -m
```

Notice that the output is the same as if you had run `lxc shell second` then the `free -m` command from inside the `second` container.

See {ref}`run-commands` for more information.

````
% End group-tab CLI

````{group-tab} UI

Go to the {guilabel}`Terminal` tab for the `first` container. This tab provides an interactive shell into an instance.

From there, run the same commands as you did on the host:

```bash
free -m
nproc
```

Note that the total memory returned by `free -m` and the value returned by `nproc` are identical for the host system and the `first` container. This is because by default, containers inherit the resources from their host environment.

Go to the {guilabel}`Terminal` tab for the `second` container and enter the same commands. Notice that only `192 MiB` total memory and `1` CPU is available. These are the options that we configured for this container earlier.

You can try other commands to interact with your instance. For example, enter the following command to display information about the operating system:

```bash
cat /etc/*release
```

Or have some fun:

```bash
apt update
apt install fortune
/usr/games/fortune
```

````
`````

(tutorial-files)=
## Access files

To access files inside an instance from your host system, use the CLI.

As an example, let's create a file in the `first` container, pull it out to the host system, modify it, then push it back to the container.

From the host system, use `lxc exec` to create an empty `helloworld` file in the `first` container:

```bash
lxc exec first -- touch helloworld.txt
```

Confirm that the file is empty:

```bash
lxc exec first -- cat helloworld.txt
```

Since the `touch` command creates an empty file, the `cat` command should display no output.

Pull this file from the `first` container to the current directory of your host system:

```bash
lxc file pull first/root/helloworld.txt .
```

Add content to the file:

```bash
echo "Hello world" > helloworld.txt
```

Push the file back to the container:

```bash
lxc file push helloworld.txt first/root/helloworld.txt
```

Now again view the content of the file on the container:

```bash
lxc exec first -- cat helloworld.txt
```

You should see the line that you added:

```{terminal}
:input: lxc exec first -- cat helloworld.txt
:user: your-user
:host: host-system

Hello world!
```

See {ref}`instances-access-files` for more information.

(tutorial-snapshots)=
## Back up and restore instances by creating snapshots

You can back up your instance by creating a snapshot, then use it later to restore the instance to a saved state.

`````{tabs}
````{group-tab} CLI

The following command creates a snapshot called "clean" that saves the current state of your instance. Run:

```bash
lxc snapshot first clean
```

Let's see how many snapshots are available for the `first` container:

```bash
lxc list first
```

The `SNAPSHOTS` column shows the number of available snapshots.

Let's find out more information about the available snapshots for the `first` container:

```bash
lxc info first
```

At the bottom of the output, a `Snapshots` table displays details about available snapshots.

If you accidentally do something to break an instance, or wish to revert recent changes to it, you can restore a previous state through a snapshot. To see how this works, let's deliberately break the `first` container by deleting the `bash` command from it:

```bash
lxc exec first -- rm /usr/bin/bash
```

Confirm that you can no longer use the bash command on `first`:

```bash
lxc exec first -- bash
```

This results in an error because the `bash` command no longer exists. Luckily, we have a snapshot we can use to restore the container to a previous state. Run:

```bash
lxc restore first clean
```

Confirm that you can now enter the `bash` shell:

```bash
lxc exec first -- bash
```

Then exit the shell:

```bash
exit
```

When you no longer need a snapshot, you can delete it. Go ahead and delete the `clean` snapshot:

```bash
lxc delete first/clean
```

````
% End group-tab CLI

````{group-tab} UI

Go to the instance detail page of the `first` container and select the {guilabel}`Snapshots` tab.

Click {guilabel}`Create snapshot` and enter the snapshot name `clean`. Leave the other options unchanged and create the snapshot. Confirm that the snapshot is now available in the {guilabel}`Snapshots` tab.

If you accidentally do something to break an instance, or wish to revert recent changes to it, you can restore a previous state through a snapshot. To see how this works, let's deliberately break the `first` container by deleting the `bash` command from it.

Go to the {guilabel}`Terminal` tab and break the container:

```bash
rm /usr/bin/bash
```

Refresh the page, and you'll see the following error:

```{figure} /images/tutorial/broken_terminal.png
:width: 100%
:alt: Error when trying to load the terminal
```

The UI cannot open a terminal for your container anymore, because you deleted the `bash` command. Luckily, we have a snapshot we can use to restore the container to a previous state.

Return to the {guilabel}`Snapshots` tab. From there, restore the container to the state of the `clean` snapshot by clicking the {guilabel}`Restore snapshot` button ({{restore_button}}) next to it.

Confirm that the container was reverted to its previous unbroken state by returning to the {guilabel}`Terminal` tab. The terminal should now load.

When you no longer need a snapshot, you can delete it. In the {guilabel}`Snapshots` tab, delete the snapshot by clicking the {guilabel}`Delete snapshot` button ({{delete_button}}) next to it.

To learn more about instance snapshots, see: {ref}`instances-snapshots`.

````
`````

(tutorial-delete)=
## Optional: Stop and delete all instances

Congratulations! You have reached the end of this tutorial and acquired a greater understanding of LXD's usage and capabilities along the way.

If you wish, you can clean up the instances you created.

```{admonition} Take caution when deleting instances
:class: warning
Deleting an instance is irreversible. All snapshots and other information associated with the instance will be lost.
```

`````{tabs}
````{group-tab} CLI

You must first stop an instance before you can delete it:

```bash
lxc stop ubuntu-vm
lxc delete ubuntu-vm
```

You can also use the `--force` flag to delete an instance without stopping it:

```bash
lxc delete ubuntu-desktop --force
```

In the same way, you can delete the other instances that you created in this tutorial (`first` and `second`).

````
% End group-tab CLI

````{group-tab} UI

Click the checkbox to the left of each instance you want to delete. Use the buttons that appear at the top of the page to first stop then delete all checked instances.
````
`````

(tutorial-snap-updates)=
## Optional: Hold snap updates

By default, snaps update automatically when a new release is published to their channel. In production environments, we strongly recommend that you disable automatic updates for the LXD snap and apply them manually. This approach allows you to schedule maintenance windows and avoid unplanned downtime.

To hold updates for the LXD snap indefinitely, run on your host machine:

```bash
sudo snap refresh --hold lxd
```

Once updates are on hold, manually update LXD regularly to benefit from security and bug fixes.

If you do not intend to run a production deployment of LXD, you might not need this. To remove the hold and restore automatic updates, run:

```bash
sudo snap refresh --unhold lxd
```

For more information on managing the LXD snap and its updates, see: {ref}`howto-snap`.

(tutorial-next)=
## Next steps

Now that you've completed your first steps with LXD, you have a general idea of how LXD works. Next, read up on important concepts in the {ref}`explanation` section and check out more advanced use cases in our {ref}`howtos`. You can also find a wealth of information in the {ref}`reference` section, including the {doc}`../api`.
