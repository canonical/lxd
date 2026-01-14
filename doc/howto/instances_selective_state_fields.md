# Optimize instance queries

LXD automatically optimizes `lxc list` queries by fetching only the state information needed for the requested columns. This significantly improves performance when listing instances, especially in large deployments.

## How it works

When you run `lxc list` with specific columns, LXD analyzes which state fields are needed:

- **Disk-dependent columns** (D): Triggers disk usage queries
- **Network-dependent columns** (4, 6): Fetches network information only
- **Other state columns** (p, N, m, M, u): Fetches minimal state without disk or network

This optimization is automatic and requires no configuration.

## Performance impact

The performance improvement is most noticeable with:
- Large numbers of instances (50+)
- Slow storage backends (NFS, network storage)
- Columns that don't require disk information

## CLI examples

```bash
# Fast - no disk or network queries
lxc list -c p          # PID only
lxc list -c N          # Process count only
lxc list -c nspt       # Name, state, PID, type

# Fetches network information only (no disk)
lxc list -c 4          # IPv4 addresses
lxc list -c 46         # IPv4 and IPv6 addresses

# Fetches disk information (slower)
lxc list -c D          # Disk usage
lxc list -c nsDt       # Name, state, disk, type
```

## API usage

The API supports selective field syntax using bracket notation:

```bash
# Fetch only disk information
curl "https://lxd-server:8443/1.0/instances?recursion=[state.disk]"

# Fetch only network information
curl "https://lxd-server:8443/1.0/instances?recursion=[state.network]"

# Fetch both disk and network
curl "https://lxd-server:8443/1.0/instances?recursion=[state.disk,state.network]"

# Fetch no state fields (fastest)
curl "https://lxd-server:8443/1.0/instances?recursion=[]"
```

Available fields:
- `state.disk` - Disk usage information
- `state.network` - Network interface information

