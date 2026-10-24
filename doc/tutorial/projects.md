(tutorial-projects)=
# Manage instances with projects

In the {ref}`previous part of the tutorial <tutorial-instances>`, you learned how to create and work with instances.

When you are managing lots of instances providing different services, it can become confusing to see which instances are dependent on each other.

The projects feature is designed to help in this situation by providing the ability to group one or more instances together into related "projects" that can be used with the CLI or the UI.

This part of the tutorial will show how to create LXD projects, add instances into a project, and explore the features that can be specified at a project level.

(tutorial-project-create)=
## Create a project

Imagine we have a scenario where we are setting up a web site for a particular client.
We want to create two instances: one for the web service and one for the database service.
Because we have lots of different web sites for different clients we want to ensure that we can quickly see and manage all of the containers for this particular web site.

Projects help us in this situation because we can create a project called `client-website` and use that project to organize instances related to the client's web site.

`````{tabs}
````{group-tab} CLI
Run this command, using the `--config` flag twice to set two options:

```bash
lxc project create client-website --config features.images=false --config features.profiles=false
```
````
````{group-tab} UI
Expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project` at the bottom.

Enter `client-website` for the name.
Under "Isolate the following features", uncheck `Images` and `Profiles`, then click {guilabel}`Create`.
````
`````

We have set the {config:option}`project-features:features.images` and {config:option}`project-features:features.profiles` options to `false` so that our new project will still use the images and profiles of the default project and will not namespace them (we'll discuss that more later in the tutorial).

`````{tabs}
````{group-tab} CLI
Now we can switch projects:

```bash
lxc project switch client-website
```

Let's check that we have switched to the new empty project:

```bash
lxc list
```

We would expect to see an empty list at this point.
````
````{group-tab} UI
After we create the `client-website` project, we see an empty list of instances.
````
`````

## Create instances in a project

Now that we have an empty project, we can go ahead and create our two containers (the web server and the database server).

`````{tabs}
````{group-tab} CLI
```bash
lxc launch ubuntu:26.04 webserver
lxc launch ubuntu:26.04 dbserver
```

Let's check that the containers are running:

```bash
lxc list
```

We should expect to see output similar to this:

```none
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
|   NAME    |  STATE  |         IPV4         |                     IPV6                      |   TYPE    | SNAPSHOTS |
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
| dbserver  | RUNNING | 10.16.179.194 (eth0) | fd42:129e:46f0:c6b3:216:3eff:fe4d:9b80 (eth0) | CONTAINER | 0         |
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
| webserver | RUNNING | 10.16.179.153 (eth0) | fd42:129e:46f0:c6b3:216:3eff:fe45:1367 (eth0) | CONTAINER | 0         |
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
```
````
````{group-tab} UI
Select {guilabel}`Instances` from the main navigation, then click {guilabel}`Create instance`.

In the form that opens, name the instance `dbserver`.
Click {guilabel}`Browse images` and select the `Ubuntu 26.04 LTS` image, then click {guilabel}`Create and start`.

Repeat these steps to create an instance named `webserver`.
````
`````

## Manage projects and instances

`````{tabs}
````{group-tab} CLI
Sometimes you don't know which project you want to work in, so LXD allows you to list the projects we have:

```bash
lxc project list
```

You should see output that looks like this:

```none
+--------------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
|           NAME           | IMAGES | PROFILES | STORAGE VOLUMES | STORAGE BUCKETS | NETWORKS | NETWORK ZONES |     DESCRIPTION     | USED BY |
+--------------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
| client-website (current) | NO     | NO       | YES             | YES             | NO       | NO            |                     | 4       |
+--------------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
| default                  | YES    | YES      | YES             | YES             | YES      | YES           | Default LXD project | 16      |
+--------------------------+--------+----------+-----------------+-----------------+----------+---------------+---------------------+---------+
```

By default, `lxc` commands affect the current project, which is the project with `(current)` after its name.
````
````{group-tab} UI
To see all of the projects available, expand the {guilabel}`Project` drop-down.
````
`````

The `default` project is the project that LXD uses for resources (such as containers, profiles, and images) that do not belong to any other project.

At this point let's reflect on how our project behaves in relation to the other projects by experimenting with the ways that we can create and list instances.

Let's switch back to the default project (this is the project that LXD uses normally), and create a new container:

`````{tabs}
````{group-tab} CLI
```bash
lxc project switch default
lxc launch ubuntu:26.04 webserver
```

You'll encounter an error message:

```none
Launching webserver
Error: Failed start validation for device "eth0": Instance DNS name "webserver" already used on network
```

````
````{group-tab} UI
Expand the {guilabel}`Project` drop-down, and select `default`.
You'll return to the {guilabel}`Instances` page.

Select {guilabel}`+ Create instance`, and create a container named `webserver` using the Ubuntu 26.04 LTS image.
Click {guilabel}`Create and start` to launch the instance.

You'll encounter an error message:

> **Instance creation failed**
>
> Failed start validation for device "eth0": Instance DNS name "webserver" already used on network
````
`````

Since we created `client-website` with {config:option}`project-features:features.profiles` set to `false`, both projects use the same profile with the same bridge network, `lxdbr0`.
Instances on the same network must have unique names, and LXD will not start an instance that share a name with another instance on the network.

This means that we can create instances with the same name, one in each project, but we cannot start the instances.

`````{tabs}
````{group-tab} CLI
Let's list instances in the current `default` project:

```bash
lxc list
```

You should expect to see a list of instances that includes the stopped `webserver` we just created.
You should *not* see the two `client-website` containers we created earlier.

```none
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
|      NAME      |  STATE  |          IPV4          |                       IPV6                       |      TYPE       | SNAPSHOTS |
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
| first          | RUNNING | 10.16.179.231 (eth0)   | fd42:129e:46f0:c6b3:216:3eff:fe4d:ccb3 (eth0)    | CONTAINER       | 0         |
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
| second         | RUNNING | 10.16.179.221 (eth0)   | fd42:129e:46f0:c6b3:216:3eff:fe02:d2b3 (eth0)    | CONTAINER       | 0         |
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
| ubuntu-desktop | RUNNING | 10.16.179.250 (enp5s0) | fd42:129e:46f0:c6b3:f679:6980:8e31:dffe (enp5s0) | VIRTUAL-MACHINE | 0         |
|                |         |                        | fd42:129e:46f0:c6b3:216:3eff:fe1c:fd2a (enp5s0)  |                 |           |
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
| ubuntu-vm      | RUNNING | 10.16.179.45 (enp5s0)  | fd42:129e:46f0:c6b3:216:3eff:fe89:811e (enp5s0)  | VIRTUAL-MACHINE | 0         |
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
| webserver      | STOPPED |                        |                                                  | CONTAINER       | 0         |
+----------------+---------+------------------------+--------------------------------------------------+-----------------+-----------+
```

Now let's list the instances in our `client-website` project, but without switching into it:

```bash
lxc list --project client-website
```

You should see the `dbserver` and `webserver` instances:

```none
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
|   NAME    |  STATE  |         IPV4         |                     IPV6                      |   TYPE    | SNAPSHOTS |
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
| dbserver  | RUNNING | 10.16.179.194 (eth0) | fd42:129e:46f0:c6b3:216:3eff:fe4d:9b80 (eth0) | CONTAINER | 0         |
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
| webserver | RUNNING | 10.16.179.153 (eth0) | fd42:129e:46f0:c6b3:216:3eff:fe45:1367 (eth0) | CONTAINER | 0         |
+-----------+---------+----------------------+-----------------------------------------------+-----------+-----------+
```
````
````{group-tab} UI
On the {guilabel}`Instances` page, you'll see that LXD created, but did not start, the `webserver` container.
You should *not* see the two `client-website` containers we created earlier.

Use the {guilabel}`Project` drop-down to return to the `client-website` project.
````
`````

You can see that we have two instances named `webserver` inside different projects, but, due to the shared profile and network, we cannot start instances with the same name as another instance.
Next, we will create a more isolated project in which we can run an instance with the same name as an instance in another project.

## Create further isolated projects

So far we have seen how a project can be used to organize instances.
However, both the `default` and `client-website` projects shared the same profiles and image libraries.
This means that the `default` profile in the `client-website` project is the same as the `default` profile in the `default` project.
But there are scenarios in which we may want to further isolate a project such that it does not share profiles or image names.

### Add a new client project

Let's imagine we have now taken on a new client and we need to create a new project for them.
However, this client requires a different network configuration than the previous client required.

We could create a new global profile called `client2-default` with the required networking configuration and then assign that profile to the instances in this new project.

However, this would mean that the `client2-default` profile would be accessible to all instances that shared the `default` project (including our previously created `client-website` project).
This would be confusing.

Instead, let's create a new project called `client2-website`, but this time we won't set {config:option}`project-features:features.images` or {config:option}`project-features:features.profiles` to `false`.

`````{tabs}
````{group-tab} CLI
```bash
lxc project create client2-website
```

Now let's create a container in the new project.
We can use the `--project` flag to create a resource in the specified project, even when another project is "current."

```bash
lxc launch ubuntu:26.04 webserver --project client2-website
```

Oops! Something went wrong though, you will get this output:

```none
Launching webserver
Error: Failed creating instance record: Failed initialising instance: Failed getting root disk: No root disk device found
```

Continue to the next step to find out what is going wrong!
````
````{group-tab} UI
Expand the {guilabel}`Project` drop-down and select {guilabel}`+ Create project`.
Enter `client2-website` for the project name.

Expand the "Default profile network" drop-down and select `No network`.
Notice that the "Default instance storage pool" is set to `default (dir)`.

Click {guilabel}`Create`, leaving the `Images` and `Profiles` features checked.

Now let's create containers in the new project.
Click {guilabel}`Create instance`, and create a container named `webserver` using the Ubuntu 26.04 LTS image.
Click {guilabel}`Create and start` to launch the instance.
Then repeat these steps to create a container named `dbserver`.

Both instances should launch successfully.

Click anywhere on the `dbserver` row to open the summary panel.
You'll see that the instance has not been assigned IPv4 or IPv6 addresses.
````
`````

## Set up a new project profile

In the previous step we created a new isolated project and then tried to create new instances inside it.

If you used the CLI, then, unfortunately, you were unable to create the instance because we got a `No root disk device found` error.
This is because the `default` profile within the new project has not been configured yet.

If you used the UI, then you were able to create instances, because the UI configures a root device for you, but the instances were not attached to a network.
We therefore still need to finish configuring the `default` project within the new project.

Let's do so now.

### Compare profiles

We can compare the `default` and `client2-website` profiles.

`````{tabs}
````{group-tab} CLI
First, let's look at the `default` profile in the `default` project.
We can use the `--project` flag again, to specify the `default` project.

```{terminal}
lxc profile show default --project default

name: default
description: Default LXD profile
config: {}
devices:
  eth0:
    name: eth0
    network: lxdbr0
    type: nic
  root:
    path: /
    pool: default
    type: disk
used_by:
- /1.0/instances/first
- /1.0/instances/second
- /1.0/instances/ubuntu-vm
- /1.0/instances/ubuntu-desktop
- /1.0/instances/webserver?project=client-website
- /1.0/instances/dbserver?project=client-website
- /1.0/instances/webserver
project: default
```

Now let's look at the `default` profile in the `client2-website` project:

Use the `--project` flag again:

```{terminal}
lxc profile show default --project client2-website

name: default
description: Default LXD profile for project client2-website
config: {}
devices: {}
used_by: []
project: client2-website
```

We can see that the missing configuration is for the devices section, specifically the `nic` and `disk` devices.
````
````{group-tab} UI
First let's look at the `default` profile in the `default` project.
Use the {guilabel}`Project` drop-down to select the project, then select {guilabel}`Profiles` from the navigation menu.

Click on `default` to see the detail page for that profile.
You'll find that "Networks" shows `lxdbr0` with an `eth0` interface, and "Devices" shows the `root` disk.

Now let's look at the `default` profile in the `client2-website` project.
Use the {guilabel}`Project` drop-down to return to the `client2-website` project, then follow the same steps to open the detail page for the project's `default` profile.
You'll find that "Networks" is empty, though "Devices" still has a `root` disk.
````
`````

### Add devices to profiles

`````{tabs}
````{group-tab} CLI
Let's add a `root` disk device sharing the global `default` storage pool:

```bash
lxc profile device add default root disk path=/ pool=default --project client2-website
```

Next we can add an `eth0` NIC device to the default profile.
To demonstrate that we can use different `default` profiles in different projects, let's create a second bridge network for this profile.
First, create the network:

```bash
lxc network create lxdbr1
```

Then attach the network to the profile:

```bash
lxc profile device add default eth0 nic name=eth0 network=lxdbr1 --project client2-website
```

Now let's try again to create the containers for this project.

```bash
lxc launch ubuntu:26.04 webserver --project client2-website
lxc launch ubuntu:26.04 dbserver --project client2-website
```

And check that they are created:

```{terminal}
lxc list --project client2-website

+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
|   NAME    |  STATE  |         IPV4          |                     IPV6                     |   TYPE    | SNAPSHOTS |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
| dbserver  | RUNNING | 10.213.141.4 (eth0)   | fd42:22e2:9fe:b470:216:3eff:fe9b:4389 (eth0) | CONTAINER | 0         |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
| webserver | RUNNING | 10.213.141.133 (eth0) | fd42:22e2:9fe:b470:216:3eff:fe44:bd1e (eth0) | CONTAINER | 0         |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
```
````
````{group-tab} UI
Next we can add an `eth0` NIC device to the default profile.

To demonstrate that we can use different `default` profiles in different projects, let's create a second bridge network for this profile.
Select {guilabel}`Networking` from the navigation menu, then select {guilabel}`Networks`.
Click {guilabel}`+ Create network`, then, in the form, enter `lxdbr1` for the name and click {guilabel}`Create`.

To attach the network to the `default` profile, select {guilabel}`Profiles` from the navigation menu, then click `default` again.
From the detail page, select the {guilabel}`Configuration` tab, then select {guilabel}`Network` from the sub-menu.

Click {guilabel}`+ Attach network`.
Then, in the form, expand the {guilabel}`Network` drop-down and select `lxdbr1`.
Click {guilabel}`Apply Changes` and then click {guilabel}`Save 1 change`.

Return to the {guilabel}`Overview` tab to see that {guilabel}`Networks` now lists `lxdbr1`.

Select {guilabel}`Instances` from the navigation menu, then click on the `dbserver` row.
You'll now find that IPv4 and IPv6 addresses have been assigned to the instance.
````
`````

## Move instances between projects

So far we have seen how instances can be grouped together and isolated to varying levels.
The last feature we can explore is how to move an instance from one project to another.
Let's move `dbserver` from the `client-website` project into the `client2-website` project.
First we need to stop the instance:

`````{tabs}
````{group-tab} CLI
```bash
lxc stop dbserver --project client-website
```
````
````{group-tab} UI
Use the {guilabel}`Project` drop-down to switch to the `client-website` project.
On the {guilabel}`Instances` page, hover the cursor over the `dbserver` row, then click the stop button.
````
`````

Now we can try to move it:

`````{tabs}
````{group-tab} CLI
```bash
lxc move dbserver dbserver --project client-website --target-project client2-website
```

But we have got an error again:

```bash
Error: Migration operation failure: Failed creating instance record: Instance "dbserver" already exists
```
````
````{group-tab} UI
Click on `dbserver` to open the detail page.

Click on the {guilabel}`Migrate` button, then select "Move instance to a different project".
Click on the {guilabel}`Select` button next to `client2-website`, and click {guilabel}`Migrate`.

But we have got an error again:

> **Project move failed for instance dbserver**
>
> `dbserver` Failed creating instance record: Instance "dbserver" already exists
````
`````

This is because you cannot have two instances with the same name in the same project, and we already have a container named `dbserver` in the `client2-website` project.

There are two courses of action here: either delete the existing `dbserver` instance, or move the instance into the project using a different name.
We'll choose the latter option in this tutorial.

`````{tabs}
````{group-tab} CLI
```bash
lxc move dbserver dbserver2 --project client-website --target-project client2-website
```
````
````{group-tab} UI
From the detail page for `dbserver`, click the instance name at the top of the page.
Enter the name `dbserver2` and click {guilabel}`Save`.

Click on the {guilabel}`Migrate` button again. Select "Move instance to a different project", select `client2-website`, and click {guilabel}`Migrate`.
````
`````

Now let's confirm that the container has been moved:

`````{tabs}
````{group-tab} CLI
```{terminal}
lxc list --project client2-website

+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
|   NAME    |  STATE  |         IPV4          |                     IPV6                     |   TYPE    | SNAPSHOTS |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
| dbserver  | RUNNING | 10.213.141.4 (eth0)   | fd42:22e2:9fe:b470:216:3eff:fe9b:4389 (eth0) | CONTAINER | 0         |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
| dbserver2 | STOPPED |                       |                                              | CONTAINER | 0         |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
| webserver | RUNNING | 10.213.141.133 (eth0) | fd42:22e2:9fe:b470:216:3eff:fe44:bd1e (eth0) | CONTAINER | 0         |
+-----------+---------+-----------------------+----------------------------------------------+-----------+-----------+
```
````
````{group-tab} UI
You should have been switched to the `client2-website` project automatically; check the {guilabel}`Project` drop-down to confirm.

Next, select {guilabel}`Instances` from the navigation menu.
Both `dbserver` and the migrated `dbserver2` should appear in the list of instances.
````
`````

## Clean up

```{warning}
Deleting an instance or project is irreversible.
All snapshots and other information associated with instances will be lost.
```

As a final task, let's clean up the instances and projects we created.
For the `client2-website` project, let's remove the instances first.
We'll also remove the images, because, with {config:option}`project-features:features.images` set to `true`, the project has its own images.

`````{tabs}
````{group-tab} CLI
You must first stop an instance before you can delete it:

```bash
lxc stop dbserver --project client2-website
lxc delete dbserver --project client2-website
```

You can also use the `--force` flag to delete an instance without stopping it:

```bash
lxc delete dbserver2 --force --project client2-website
lxc delete webserver --force --project client2-website
```

````
````{group-tab} UI
Click the checkbox in the header row to select all instances.
Use the buttons that appear at the top of the page to first stop then delete all checked instances.
````
`````

Next, let's delete any images created in the project:

`````{tabs}
````{group-tab} CLI
```bash
lxc image list --project client2-website
```

For each image do:

```bash
lxc image delete <fingerprint> --project client2-website
```
````
````{group-tab} UI
Select {guilabel}`Images` from the navigation menu, then select {guilabel}`Local images`.
Check the box in the header row of the table to select all images, then click the {guilabel}`Delete image` button that appears.
````
`````

Then delete the project:

`````{tabs}
````{group-tab} CLI
```bash
lxc project delete client2-website
```
````
````{group-tab} UI
Select {guilabel}`Configuration` from the navigation menu.
Click the {guilabel}`Delete project` button, enter the name of the project, and click {guilabel}`Permanently delete`.
````
`````

You can also delete a project that still has resources.

`````{tabs}
````{group-tab} CLI
This time, use the `--force` flag to delete the `client-website` project:

```bash
lxc project delete client-website --force
```

````
````{group-tab} UI
Use the {guilabel}`Project` dropdown to switch to the `client-website` project.
Select {guilabel}`Configuration` from the navigation menu, then click the {guilabel}`Delete project` button.
The LXD UI will list resources that will be deleted with the project.
Enter the project name and click {guilabel}`Permanently delete`.
````
`````

This deletes the project's instances.
However, because we created `client-website` with {config:option}`project-features:features.images` set to `false`, the project used the images and profiles of the `default` project.
As a result, those images and profiles were not deleted.

Finally, let's clean up the remaining instances in the `default` project.

`````{tabs}
````{group-tab} CLI
You can delete multiple instances at once:

```bash
lxc delete --force first second ubuntu-vm ubuntu-desktop
```
````
````{group-tab} UI
From the {guilabel}`Instances` page of the `default` project, click the checkbox to the left of the instance you want to delete.
Use the buttons that appear at the top of the page to first stop then delete all checked instances.
````
`````

## Next steps

Now that you've completed your first steps with LXD, you have a general idea of how LXD works.
Next, read up on important concepts in the {ref}`explanation` section and check out more advanced use cases in our {ref}`howtos`.
You can also find a wealth of information in the {ref}`reference` section, including the {doc}`../api`.
