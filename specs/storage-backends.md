# Storage Backends and supported functions

LXD supports using plain dirs, LVM, and btrfs for storage of images and containers.
Where possible, LXD tries to use the advanced features of each system to optimize operations.

The following table shows which operations are optimized for each backend:

| Storage Type | Image Create | Container Create                                            | Container Local Copy                  | Snapshot Create          | Remote Copy                               |
|--------------|--------------|-------------------------------------------------------------|---------------------------------------|--------------------------|-------------------------------------------|
| LVM          |              | uses LV snapshot from image (creates image LV if necessary) | TODO (throws error)                   | TODO (throws error)      | rsync                                     |
| btrfs        |              | creates subvol of image (creates image subvol if necessary) | subvol-snapshot if source is snapshot | readonly subvol-snapshot | TODO - rsync, should use btrfs primitives |
|              |              |                                                             |                                       |                          |                                           |

