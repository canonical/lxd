(tutorial-ui)=
# Getting started with the UI

This tutorial gives a quick introduction to using the LXD UI.
It covers installing and initializing LXD, getting access to the UI, and carrying out some standard operations like creating, configuring, and interacting with instances, configuring storage, and using projects.

After going through these steps, you will have a general idea of how to use LXD through its UI, and you can start exploring more advanced use cases!

```{note}
Ensure that you have 20 GiB free disk space before starting this tutorial.
```

% Include content from [first_steps.md](first_steps.md)
```{include} first_steps.md
    :start-after: <!-- Include start tutorial installation -->
    :end-before: <!-- Include end tutorial installation -->
```

## Access the UI

You access the LXD UI through your browser.
See {ref}`access-ui` for more information.

1. Expose LXD to the network by setting the {config:option}`server-core:core.https_address` server configuration option:

       lxc config set core.https_address :8443

% Include content from [../howto/access_ui.md](../howto/access_ui.md)
```{include} ../howto/access_ui.md
    :start-after: <!-- Include start access UI -->
    :end-before: <!-- Include end access UI -->
```

## Create and start instances

Let's start by launching a few instances.
With *instance*, we mean either a container or a virtual machine.
See {ref}`containers-and-vms` for information about the difference between the two instance types.

1. Select {guilabel}`Instances` from the navigation.
1. Click {guilabel}`Create instance` to launch a container called `first` using the Ubuntu 24.04 LTS image:

   ```{figure} /images/tutorial/create_instance.png
   :width: 100%
   :alt: Create an Ubuntu 24.04 LTS container
   ```

   To select the base image, click {guilabel}`Browse images`.
   Click {guilabel}`Select` next to the Ubuntu 24.04 LTS image.

   ```{note}
   The images that are displayed are hosted on pre-configured {ref}`remote-image-servers`.
   You can filter which images are displayed.

   You can also upload a custom ISO file to boot from.
   See {ref}`instances-create-iso` for more information.
   ```

1. To launch the container, click {guilabel}`Create and start`.

   ```{note}
   Launching this container takes a few seconds, because the image must be downloaded and unpacked first.
   ```

1. Create another container called `second`, using the same image.
   After entering the name and selecting the image, click {guilabel}`Create` instead of {guilabel}`Create and start`.

   This container will be created but not started.

   ```{note}
   Creating this container is quicker than launching the first, because the image is already available locally.
   ```

1. Create and start a VM called `ubuntu-vm` using the Ubuntu 24.04 LTS image.
   To create a VM instead of a container, select {guilabel}`VM` as the instance type:

   ```{figure} /images/tutorial/create_vm.png
   :width: 100%
   :alt: Create an Ubuntu 24.04 LTS VM
   ```

   ```{note}
   Even though you are using the same image name to launch the instance, LXD downloads a slightly different image that is compatible with VMs.
   ```

1. Start creating (do not click {guilabel}`Create and start` yet) a VM called `ubuntu-desktop`.
   When selecting the image, filter by variant "desktop" to find the Ubuntu 24.04 LTS desktop image.
   Note that after you select the image, the instance type is automatically set to {guilabel}`VM`:

   ```{figure} /images/tutorial/create_desktop_vm.png
   :width: 100%
   :alt: Create an Ubuntu 24.04 LTS desktop VM
   ```

   To run smoothly, the desktop VM needs more RAM.
   Therefore, navigate to {guilabel}`Advanced` > {guilabel}`Resource limits` and set the {guilabel}`Memory limit` to 4 GiB.
   Then click {guilabel}`Create and start` to start the instance.

1. Check the list of instances that you created:

   ```{figure} /images/tutorial/instances.png
   :width: 100%
   :alt: List of instances
   ```

   You will see that all but the `second` container are running.
   This is because you created the `second` container but didn't start it.

   You can start the `second` container by clicking the {guilabel}`Start` button (▷) next to it.

See {ref}`instances-create` for more information.

## Inspect instances

In the list of instances, click on one of the lines to see more information about the respective instance:

```{figure} /images/tutorial/instance_summary.png
:width: 100%
:alt: Information about an instance in the instance summary
```

Click on an instance name to go to the instance detail page, where you can inspect your instance:

- The {guilabel}`Overview` tab shows general information and usage statistics about the instance.
- The {guilabel}`Configuration` tab contains the instance configuration.
  For now, just click through to inspect the configuration.
  We'll do some updates later.

  If you want to see the full instance configuration, go to the YAML configuration:

   ```{figure} /images/tutorial/yaml_configuration.png
   :width: 100%
   :alt: YAML configuration of an instance
   ```

- The {guilabel}`Snapshots` tab shows available snapshots.
  You can ignore this for now; we'll look into snapshots later.
- The {guilabel}`Terminal` tab allows you to interact with your instance.
  For example, enter the following command to display information about the operating system:

      cat /etc/*release

  Or have some fun:

      apt update
      apt install fortune
      /usr/games/fortune

  See {ref}`run-commands-shell` for more information.

  ```{note}
  When you navigate away from the {guilabel}`Terminal` tab, you are asked to confirm.
  The reason for this confirmation prompt is that the terminal session is not saved, so once you navigate away, your command history is lost.
  ```

- The {guilabel}`Console` tab is mainly relevant during startup of an instance, and for VMs.

  Go to the instance detail page of the `ubuntu-desktop` VM and check the graphic console:

   ```{figure} /images/tutorial/desktop_console.png
   :width: 100%
   :alt: Graphic console of the Ubuntu desktop VM
   ```

  See {ref}`instances-console` for more information.
- The {guilabel}`Logs` tab contains log files for inspection and download.
  Running instances have only limited information.
  More log files are added if an instance ends up in error state.

  See {ref}`instances-troubleshoot` for more information.

## Stop and delete instances

For the remainder of the tutorial, we don't need all of the instances we created.
So let's clean some of them up:

1. Go back to the instances list.
1. Stop the `second` container by clicking the {guilabel}`Stop` button (□) next to it.
1. Delete the `second` container.
   To do so, click the instance name to go to the instance detail page.
   Then click the {guilabel}`Delete instance` button at the top.
1. Go to the instance detail page of the `ubuntu-vm` VM to delete it.

   You will see that the {guilabel}`Delete instance` button at the top is not active.
   This is because the instance is still running.

   Stop it by clicking the {guilabel}`Stop` button (□) at the top, then click {guilabel}`Delete instance`.

```{tip}
If stopping an instance takes a long time, click the spinning {guilabel}`Stop` button to go back to the confirmation prompt, where you can select to force-stop the instance.
```

See {ref}`instances-manage` for more information.

## Configure instances

There are several limits and configuration options that you can set for your instances.
See {ref}`instance-options` for an overview.

Let's create another container with some resource limits:

1. On the instances list, click {guilabel}`Create instance`.
   Enter `limited` as the instance name and select the Ubuntu 24.04 LTS image.

1. Expand {guilabel}`Advanced` and go to {guilabel}`Resource limits`.

1. Change the {guilabel}`Exposed CPU limit` to 1 and the {guilabel}`Memory limit` to 4 GiB:

   ```{figure} /images/tutorial/resource_limits.png
   :width: 100%
   :alt: Configure some resource limits
   ```

1. Click {guilabel}`Create and start`.

1. When the instance is running, go to its instance detail page and select {guilabel}`Configuration` > {guilabel}`Advanced` > {guilabel}`Resource limits`.
   Confirm that the limits you set are visible.

1. Go to the {guilabel}`Terminal` tab and enter the following commands:

       free -m
       nproc

   You should see that the total memory is limited to 4096, and the number of available CPUs is 1.

1. Go to the instance detail page of the `first` container and enter the same commands.

   You should see that the values differ from those of the `limited` container.
   The exact values depend on your host system (so if your host system has only one CPU, for example, you might see one CPU for the `first` container as well).

1. You can also update the configuration while your container is running:

   1. Go back to the instance detail page of the `limited` container and select {guilabel}`Configuration` > {guilabel}`Advanced` > {guilabel}`Resource limits`.

   1. Click {guilabel}`Edit instance` and change the memory limit to 2 GiB.
      Then save.

   1. Go to the {guilabel}`Terminal` tab and enter the following command:

          free -m

      Note that the number has changed.

Depending on the instance type and the storage drivers that you use, there are more configuration options that you can specify.
For example, you can configure the size of the root disk device for a VM:

1. Go to the terminal for the `ubuntu-desktop` VM and check the current size of the root disk device (`/dev/sda2`):

       df -h

1. Navigate to {guilabel}`Configuration` > {guilabel}`Advanced` > {guilabel}`Disk devices`.

   ```{tip}
   By default, the size of the root disk is inherited from the default profile.
   Profiles store a set of configuration options and can be applied to instances instead of specifying the configuration option for each instance separately.

   See {ref}`profiles` for more information.
   ```

1. Override the size of the root disk device to be 30 GiB:

   ```{figure} /images/tutorial/root_disk_size.png
   :width: 100%
   :alt: Change the size of the root disk
   ```

1. Save the configuration, and then restart the VM by clicking the {guilabel}`Restart` button ({{restart_button}}).

1. In the terminal, check the size of the root disk device again:

       df -h

   Note that the size has changed.

See {ref}`instances-configure` and {ref}`instance-config` for more information.

## Manage snapshots

You can create a snapshot of your instance, which makes it easy to restore the instance to a previous state.

1. Go to the instance detail page of the `first` container and select the {guilabel}`Snapshots` tab.

1. Click {guilabel}`Create snapshot` and enter the snapshot name `clean`.
   Leave the other options unchanged and create the snapshot.

   You should now see the snapshot in the list.

1. Go to the {guilabel}`Terminal` tab and break the container:

       rm /usr/bin/bash

1. Confirm the breakage by reloading the page:

   ```{figure} /images/tutorial/broken_terminal.png
   :width: 100%
   :alt: Error when trying to load the terminal
   ```

   The UI cannot open a terminal in your container anymore, because you deleted the `bash` command.

1. Go back to the {guilabel}`Snapshots` tab and restore the container to the state of the snapshot by clicking the {guilabel}`Restore snapshot` button ({{restore_button}}) next to it.

1. Go back to the {guilabel}`Terminal` tab.
   The terminal should now load again.

1. Go to the {guilabel}`Snapshots` tab and delete the snapshot by clicking the {guilabel}`Delete snapshot` button ({{delete_button}}) next to it.

See {ref}`instances-snapshots` for more information.

## Add a custom storage volume

You can add additional storage to your instance, and also share storage between different instances.
See {ref}`storage-volumes` for more information.

Let's start by creating a custom storage volume:

1. Navigate to {guilabel}`Storage` > {guilabel}`Volumes`.

   Even though you have not created any custom storage volumes yet, you should see several storage volumes in the list.
   These are instance volumes (which contain the root disks of your instances) and image volumes (which contain the cached base images).

1. Click `Create volume` and enter a name and a size.
   Leave the default content type (`filesystem`).

After creating the instance, we can attach it to some instances:

1. Go to the instance detail page of the `first` container.

1. Go to {guilabel}`Configuration` > {guilabel}`Advanced` > {guilabel}`Disk devices`.

1. Click {guilabel}`Edit instance` and then {guilabel}`Attach disk device`.

1. Select the disk device that you just created.

   ```{tip}
   You can create a custom volume directly from this screen as well.
   ```

1. Enter `/data` as the mount point and save your changes:

   ```{figure} /images/tutorial/add_disk_device.png
   :width: 100%
   :alt: Add the custom volume to your instance
   ```

1. Go to the {guilabel}`Terminal` tab and enter the following command to create a file on the custom volume:

       touch /data/hello_world

1. Go to the instance detail page of the `ubuntu-desktop` VM and add the same custom storage volume with the same mount point (`/data`).

1. Go to the {guilabel}`Terminal` tab and enter the following command to see the file you created from your `first` container:

       ls /data/

   ````{note}
   You can also look at the directory in the file browser.
   To do so, enter the following command in the terminal first:

       chown ubuntu /data /data/*

   Then switch to the console, open the file browser, and navigate to the `/data` folder:

   ```{figure} /images/tutorial/hello_world_desktop.png
   :width: 100%
   :alt: View the file on the shared volume in the file browser
   ```
   ````

## Use projects

You can use projects to group related instances, and other entities, on your LXD server.
See {ref}`exp-projects` and {ref}`projects-create` for more information.

Originally, there is only a default project on the server.
All the instances you created so far are part of this project.

Now, let's create another project:

1. Expand the {guilabel}`Project` dropdown and click {guilabel}`Create project`:

   ```{figure} /images/tutorial/create_project.png
   :width: 100%
   :alt: Create a project
   ```

1. Enter `tutorial` as the project name.

1. For features, select `Customised`.

   You can then select which features should be isolated.
   "Isolated" in this context means that if you select one of the features, entities of this type are confined to the project.
   So when you use a project where, for example, storage volumes are isolated, you can see only the storage volumes that are defined within the project.

1. Deselect `Storage volumes` and create the project.

The new project is automatically selected for you.
Let's check its content:

1. Go to {guilabel}`Instances`.

   You will see that there are no instances in your project, because instances are always isolated, and the instances you created earlier are in the default project.

1. Create an instance in the new project.

   You should notice that you get an error about missing root storage.
   The reason for this is that the root storage is usually defined in the default profile, but profiles are isolated in the project.

1. To resolve the error, edit the root storage.
   Use the `default` pool and leave the size empty.

   Then create the instance.

1. Go to {guilabel}`Storage` > {guilabel}`Volumes`.

   Remember that in the default project, you saw three different kinds of storage volumes in the volume list:

   - Instance (container or VM) volumes
   - Image volumes
   - Custom volumes

   You should see the same three types in the `tutorial` project.
   However, note the following:

   - You can see only one instance volume and one image volume.
     These are for the one instance you created in the `tutorial` project.

     You cannot see the instance and image volumes for the instances you created in the default project, because both instances and images are isolated in the `tutorial` project, so you cannot see the corresponding storage volumes from other projects.

   - You can see the custom storage volume that you created in the default project.

     Because you deselected `Storage volumes` when creating the project, storage volumes are not isolated, and you can therefore see storage volumes from other projects.

## Clean up entities

Now that we've run through the basic functionality of LXD, let's clean up the entities we created throughout the tutorial.

1. With the `tutorial` project still selected, go to the instances list and stop and delete the instance you created in this project.

1. Go to {guilabel}`Images` and click the {guilabel}`Delete` button ({{delete_button}}) next to it.

1. Go to {guilabel}`Configuration` and click the {guilabel}`Delete project` button in the top-right corner.

   After deleting the project, you are automatically switched back to the default project.

1. Stop and delete all instances in the default project.
   To do this all at once, go to the instance list and select all instances.
   Then click {guilabel}`Stop` at the top.
   Finally, click {guilabel}`Delete` at the top.

1. Go to {guilabel}`Storage` > {guilabel}`Volumes` and click the {guilabel}`Delete` button ({{delete_button}}) next to the `tutorial_volume` storage volume.

```{note}
Optionally, you can also delete the images that you used.
However, this isn't really needed.
If you keep them, they will eventually expire (by default, when they haven't been used for ten days).
```

% Include content from [first_steps.md](first_steps.md)
```{include} first_steps.md
    :start-after: <!-- Include start tutorial next steps -->
    :end-before: <!-- Include end tutorial next steps -->
```
