# Project configuration
LXD supports projects as a way to split your LXD server.
Each project holds its own set of containers and may also have its own images and profiles.

What a project contains is defined through the `features` configuration keys.
When a feature is disabled, the project inherits from the `default` project.

By default all new projects get the entire feature set, on upgrade,
existing projects do not get new features enabled.

The key/value configuration is namespaced with the following namespaces
currently supported:

 - `features` (What part of the project featureset is in use)
 - `user` (free form key/value for user metadata)

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
features.images                 | boolean   | -                     | true                      | Separate set of images and image aliases for the project
features.profiles               | boolean   | -                     | true                      | Separate set of profiles for the project


Those keys can be set using the lxc tool with:

```bash
lxc project set <project> <key> <value>
```
