---
discourse: 7362
---

(instances-troubleshoot)=
# How to troubleshoot failing instances

If your instance fails to start and ends up in an error state, this usually indicates a bigger issue related to either the image that you used to create the instance or the server configuration.

To troubleshoot the problem, complete the following steps:

1. Save the relevant log files and debug information:

   Instance log
   : Enter the following command to display the instance log:

         lxc info <instance_name> --show-log

   Console log
   : Enter the following command to display the console log:

         lxc console <instance_name> --show-log

   Detailed server information
   : The LXD snap includes a tool that collects the relevant server information for debugging.
     Enter the following command to run it:

         sudo lxd.buginfo

1. Reboot the machine that runs your LXD server.
1. Try starting your instance again.
   If the error occurs again, compare the logs to check if it is the same error.

   If it is, and if you cannot figure out the source of the error from the log information, open a question in the [forum](https://discuss.linuxcontainers.org).
   Make sure to include the log files you collected.
