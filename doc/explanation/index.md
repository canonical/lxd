(explanation)=
# Explanation

The explanatory guides in this section introduce you to the concepts used in LXD and help you understand how things fit together.

(explanation-concepts)=
## Important concepts

Before you start working with LXD, you need to be familiar with some important concepts about LXD and the instance types it provides.

```{toctree}
:titlesonly:

/explanation/lxd_lxc
/explanation/instances
```

(explanation-entities)=
## Entities in LXD

When working with LXD, you should have a basic understanding of the different entities that are used in LXD.
See the {ref}`howtos` for instructions on how to work with these entities, and the following guides to understand the concepts behind them.

```{toctree}
:titlesonly:

/image-handling
About storage </explanation/storage>
/explanation/networks
/database
```

(explanation-iam)=
## Access management

In LXD, access to the API is controlled through TLS or OpenID Connect authentication.
When using OpenID Connect, you can grant permissions to access specific entities to different clients.
You can also restrict access to LXD entities by confining them to specific projects.

```{toctree}
:titlesonly:

About authentication </authentication>
About authorization </explanation/authorization>
/explanation/projects
```

(explanation-production)=
## Production setup

When you're ready to move your LXD setup to production, you should read up on the concepts that are important for providing a scalable, reliable, and secure environment.

```{toctree}
:titlesonly:

/explanation/clustering
/explanation/performance_tuning
/explanation/security
```
