(exp-projects)=
# About projects

```{youtube} https://www.youtube.com/watch?v=cUHkgg6TovM
```

- use cases
- what you can restrict

LXD supports projects as a way to split your LXD server.
Each project holds its own set of instances and may also have its own images and profiles.

What a project contains is defined through the `features` configuration keys.
When a feature is disabled, the project inherits from the `default` project.

By default all new projects get the entire feature set, on upgrade,
existing projects do not get new features enabled.


## Confined projects in a multi-user environment

```{youtube} https://www.youtube.com/watch?v=6O0q3rSWr8A
```

### Authentication methods for projects
