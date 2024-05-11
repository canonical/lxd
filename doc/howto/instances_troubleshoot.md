---
discourse: 7362
---

(instances-troubleshoot)=
# How to troubleshoot failing instances

If your instance fails to start and ends up in an error state, this usually indicates a bigger issue related to either the image that you used to create the instance or the server configuration.

To troubleshoot the problem, complete the following steps:

1. Save the relevant log files and debug information:

   Instance log
   : Display the instance log:

     ````{tabs}
     ```{group-tab} CLI
         lxc info <instance_name> --show-log
     ```
     ```{group-tab} API
         lxc query --request GET /1.0/instances/<instance_name>/logs/lxc.log
     ```
     ```{group-tab} UI
     Navigate to the instance detail page and switch to the {guilabel}`Logs` tab to view the available log files.
     ```
     ````

   Console log
   : Display the console log:

     ````{tabs}
     ```{group-tab} CLI
         lxc console <instance_name> --show-log

     This command is available only for containers.
     ```
     ```{group-tab} API
         lxc query --request GET /1.0/instances/<instance_name>/console

     This endpoint is available only for containers.
     ```
     ```{group-tab} UI
     Navigate to the instance detail page and switch to the {guilabel}`Console` tab to view the console.
     The console is displayed only when the instance is running.
     ```
     ````

   Detailed server information
   : The LXD snap includes a tool that collects the relevant server information for debugging.
     Enter the following command to run it:

         sudo lxd.buginfo

1. Reboot the machine that runs your LXD server.
1. Try starting your instance again.
   If the error occurs again, compare the logs to check if it is the same error.

   If it is, and if you cannot figure out the source of the error from the log information, open a question in the [forum](https://discourse.ubuntu.com/c/lxd/).
   Make sure to include the log files you collected.

## Troubleshooting example

In this example, let's investigate a RHEL 7 system in which `systemd` cannot start.

```{terminal}
:input: lxc console --show-log rhel7

Console log:

Failed to insert module 'autofs4'
Failed to insert module 'unix'
Failed to mount sysfs at /sys: Operation not permitted
Failed to mount proc at /proc: Operation not permitted
[!!!!!!] Failed to mount API filesystems, freezing.
```

The errors here say that `/sys` and `/proc` cannot be mounted - which is correct in an unprivileged container.
However, LXD mounts these file systems automatically if it can.

The {doc}`container requirements <../container-environment>` specify that every container must come with an empty `/dev`, `/proc` and `/sys` directory, and that `/sbin/init` must exist.
If those directories don't exist, LXD cannot mount them, and `systemd` will then try to do so.
As this is an unprivileged container, `systemd` does not have the ability to do this, and it then freezes.

So you can see the environment before anything is changed, and you can explicitly change the init system in a container using the {config:option}`instance-raw:raw.lxc` configuration parameter.
This is equivalent to setting `init=/bin/bash` on the Linux kernel command line.

    lxc config set rhel7 raw.lxc 'lxc.init.cmd = /bin/bash'

Here is what it looks like:

```{terminal}
:input: lxc config set rhel7 raw.lxc 'lxc.init.cmd = /bin/bash'

:input: lxc start rhel7
:input: lxc console --show-log rhel7

Console log:

[root@rhel7 /]#
```

Now that the container has started, you can check it and see that things are not running as well as expected:

```{terminal}
:input: lxc exec rhel7 -- bash

[root@rhel7 ~]# ls
[root@rhel7 ~]# mount
mount: failed to read mtab: No such file or directory
[root@rhel7 ~]# cd /
[root@rhel7 /]# ls /proc/
sys
[root@rhel7 /]# exit
```

Because LXD tries to auto-heal, it created some of the directories when it was starting up.
Shutting down and restarting the container fixes the problem, but the original cause is still there - the template does not contain the required files.
