(instance-create)=
# How to create an instance

## Instance types

LXD supports simple instance types. Those are represented as a string
which can be passed at instance creation time.

The syntax allows the three following forms:

- `<instance type>`
- `<cloud>:<instance type>`
- `c<CPU>-m<RAM in GB>`

For example, those 3 are equivalent:

- `t2.micro`
- `aws:t2.micro`
- `c1-m1`

On the command line, this is passed like this:

```bash
lxc launch ubuntu:22.04 my-instance -t t2.micro
```

The list of supported clouds and instance types can be found here:

  [`https://github.com/dustinkirkland/instance-type`](https://github.com/dustinkirkland/instance-type)
