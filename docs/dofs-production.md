# DOFS production deployment

`domus dofs serve` is the Linux data plane for DOFS. It is deliberately a
separate foreground service: restarting the Domus HTTP control plane does not
tear down active filesystems, and systemd owns restart/backoff policy for the
FUSE process.

## Host contract

The initial production target is one Linux compute host with:

- `/dev/fuse` and `fusermount3` from FUSE 3;
- network access to PostgreSQL and the configured S3-compatible object store;
- a dedicated, non-login `domus` service account;
- a local encrypted volume for `dofs.state_root`;
- rootful Docker for user containers.

Use TLS-verified PostgreSQL (`sslmode: verify-full`) and HTTPS object-storage
endpoints outside a trusted single-host development network. The example
configuration's `sslmode: disable` is not a production recommendation.

When `dofs.allow_other` is enabled, `/etc/fuse.conf` must contain the following
uncommented line:

```text
user_allow_other
```

The manager checks `/dev/fuse`, `fusermount3`, and this setting before opening
its control socket. `allow_other` is necessary when dockerd or the container
UID differs from the UID running DOFS. Kernel `default_permissions` still
enforces the synthetic DOFS UID/GID and owner-only modes.

DOFS reports this flag in each mount status. Workspace Manager rejects the
handoff unless the mount is writable, desired, `allow_other`, and has the exact
configured UID/GID.

Use a service UID distinct from the container UID (normally `1000`). Keep
`dofs.mount_root` mode `0700`: dockerd can bind an exact child as root, while
ordinary host processes cannot traverse the tree and enumerate other users'
plaintext mounts.

## Filesystem layout

The recommended configuration is:

```yaml
dofs:
  mount_root: /var/lib/domus/dofs/mounts
  state_root: /var/lib/domus/dofs/state
  control_socket: /run/domus/dofs.sock
  # Workspace daemon and the Domus web process join this narrowly scoped group.
  socket_group: "domus"
  uid: 1000
  gid: 1000
  allow_other: true
  writable: true
  max_mounts: 32
  reconcile_interval_seconds: 30
  mount_timeout_seconds: 60
  shutdown_timeout_seconds: 30
```

The trees have different responsibilities:

```text
/var/lib/domus/dofs/mounts/<immutable-user-id>  plaintext FUSE mount
/var/lib/domus/dofs/state/users/<user-id>       private sparse cache and WAL
/var/lib/domus/dofs/state/desired/<user-id>.json persistent desired mount state
/run/domus/dofs.sock                            local control API
```

The state tree contains transient plaintext blocks and must remain `0700` on
an encrypted local volume. It must not live in OSS, a container layer, NFS, or
a world-readable backup. If it is backed up, stop DOFS first and protect the
snapshot like plaintext user data.

Sparse cache files consume blocks only for fetched or dirty ranges, but a full
overwrite can still consume the logical file size until publication finishes.
Monitor free space and place `state_root` on a filesystem with an operator-set
host quota. Per-user cache quotas are not implemented yet, so mount capacity is
not a substitute for disk-capacity monitoring.

The DOFS process also holds the server wrapping key and active user KEKs in
memory. The supplied systemd unit disables core dumps; do not enable process
dumping or broad ptrace access for the service account in production.

An explicit `dofs unmount` first marks the user undesired in memory, but keeps
the durable desired marker until the physical unmount and mountpoint cleanup
both succeed. A busy or failed unmount is therefore retried by reconciliation,
and a process crash cannot leave an untracked live mount. A later explicit
`ensure` cancels that pending removal and republishes the desired state.
Writeback state is retained for safe WAL recovery. Normal service shutdown
preserves desired markers, so mounts return after an upgrade or host restart.

## systemd

Install the supplied unit after creating the service account and protecting
the configuration file:

```text
deploy/systemd/domus.sysusers.conf  -> /usr/lib/sysusers.d/domus.conf
deploy/systemd/domus-dofs.service -> /etc/systemd/system/domus-dofs.service
deploy/systemd/domus.service      -> /etc/systemd/system/domus.service
/etc/domus/config.yaml            owner domus, mode 0600
```

Packages can run `systemd-sysusers` to create the account from the supplied
definition. Existing deployments may keep their existing dedicated service
account and adjust `User`, `Group`, file ownership, and `socket_group`
consistently.

Provision `server.encryption_secret`, database schema/root account, and OSS
credentials before enabling `domus-dofs.service`. The data-plane service never
generates or rewrites encryption secrets. For a fresh installation, complete
the normal Domus first-start initialization once, stop it, then enable the
DOFS unit and the regular control-plane unit in their final ordering.

The unit intentionally runs in the host mount namespace. systemd directives
such as `PrivateTmp`, `ProtectSystem`, `ProtectHome`, `RootDirectory`,
`BindPaths`, and `InaccessiblePaths` create a filesystem namespace even when
`PrivateMounts=no`; mounts created there may be invisible to dockerd. Do not
add those directives to this unit. Similarly, `NoNewPrivileges=yes` can stop
an unprivileged service from invoking the setuid `fusermount3` helper.

`RequiresMountsFor=/var/lib/domus/dofs/state` orders the service after a
separate state volume declared through fstab or a mount unit. Change the path
if `state_root` is overridden. This dependency prevents a configured volume
from being skipped; it does not prove that the backing device is encrypted.

The supplied unit uses `Type=notify`. DOFS reports ready only after database,
OSS wiring, host preflight, manager locks, and the Unix listener have all been
created. This makes `Before=domus.service` a real startup barrier instead of
merely recording process creation.

If the distribution restricts `/dev/fuse` to a `fuse` group, add `domus` to
that group or add `SupplementaryGroups=fuse` in a local unit override.

The intended ordering is:

```text
PostgreSQL and network
  -> domus-dofs.service
  -> Domus control plane / user-container manager
```

On shutdown the reverse order ensures user containers release bind mounts
before DOFS performs a normal unmount. DOFS never uses lazy detach: a busy
mount remains an explicit degraded condition instead of silently pinning a
detached plaintext filesystem.

## Control socket

The versioned HTTP API exists only on the Unix socket:

```text
GET    /v1/health/live
GET    /v1/health/ready
GET    /v1/mounts
POST   /v1/mounts
GET    /v1/mounts/<user-id>
DELETE /v1/mounts/<user-id>
POST   /v1/reconcile
```

There is no bearer token. Socket ownership is authentication. With an empty
`socket_group` it is `0600`; setting a group changes it to `0660`. Membership
in that group is highly privileged because a caller can request any user's
plaintext mount. Never expose the socket inside a user container.

The socket's parent directory must also be traversable by that trusted group.
With the supplied unit, `/run/domus` is mode `0750` and group `domus`, so use
the `domus` group (and add only the container-manager account to it), or adjust
`Group`, `RuntimeDirectoryMode`, and `socket_group` together in an override.

Equivalent operator commands are:

```bash
domus dofs health -c /etc/domus/config.yaml
domus dofs ensure -c /etc/domus/config.yaml --user root
domus dofs status -c /etc/domus/config.yaml
domus dofs unmount -c /etc/domus/config.yaml --user-id <immutable-user-id>
domus dofs reconcile -c /etc/domus/config.yaml
```

`health/live` checks the control process. `health/ready` returns HTTP 503 while
any desired mount is pending or failed, or while an orphan/foreign mount blocks
recovery. Monitor both separately; a readiness failure should not cause an
unbounded liveness restart loop.

## Docker handoff

The container manager must call `Ensure` and wait for state `mounted` before
creating the container. It then bind-mounts exactly the returned path:

```text
type=bind
source=/var/lib/domus/dofs/mounts/<immutable-user-id>
target=/workspace
bind propagation=rprivate
```

Never bind the shared `mount_root`. The user container receives neither OSS
credentials, the server encryption key, the DOFS control socket, `/dev/fuse`,
nor the Docker socket.

The FUSE mount must exist before container creation. If DOFS exits, an existing
container can retain a disconnected mount even after DOFS remounts the same
host path. The Workspace Manager stops and recreates that user's container
whenever the DOFS `mount_id` changes; it never assumes a remount becomes
visible through an existing private bind.

The repository contains an opt-in real handoff test. It never pulls an image:

```bash
DOFS_DOCKER_INTEGRATION=1 \
DOFS_DOCKER_IMAGE=domus-workspace:0.1.0 \
go test ./internal/dofs -run TestFUSEBindMountIntoDocker -v -count=1
```

## Recovery and fencing

Only one manager can own a state root because it holds a local `flock`. Each
writable user mount also holds a PostgreSQL advisory lock on a dedicated
database session. That session is checked every five seconds. If it is lost,
DOFS rejects further mutations, marks the mount unhealthy, and attempts a
normal unmount; readiness remains degraded until the old bind is released and
reconciliation can mount again.

File generation CAS and transactional namespace locks remain the final data
integrity fences during the short lease-detection window. They prevent a stale
writer from silently replacing a newer immutable generation, but two live
mounts do not provide coherent POSIX caches. Operators must therefore treat a
lease-loss alert as a container restart event.

Each active writable mount pins one PostgreSQL connection for its session
advisory lock. `dofs.max_mounts` is therefore both a host resource limit and a
database-connection safety limit. Size PostgreSQL `max_connections` for this
value plus the HTTP service and administrative headroom; do not set the DOFS
limit higher merely to hide a capacity alert.
An `Ensure` beyond the limit is durably accepted as `pending` (HTTP 202), and
readiness stays degraded until another mount is released and reconciliation
can start it.

On startup reconciliation handles only mounts under the dedicated mount root:

- a disconnected `fuse.dofs` mount is normally unmounted and recreated;
- a healthy unmanaged DOFS mount is blocked rather than stolen;
- a different filesystem at a managed path is never unmounted;
- a nonempty underlying mountpoint is never overwritten;
- symlink mountpoints and marker files are rejected.

## Multi-host boundary

The current production scope is single-host scheduling. PostgreSQL prevents
two writable mounts for one user during normal operation, but writeback WAL is
host-local plaintext state. Do not fail a user over to another host while the
old host can still run or has unrecovered WAL. A multi-host scheduler will need
host ownership/fencing plus a drain protocol before automatic failover.

The same rule applies to account deletion: stop the user's container, issue a
DOFS unmount, verify it is no longer desired, and only then delete user data.

## Operational checks

After deployment, verify:

1. `domus dofs health` reports `ready`.
2. Ensuring two users produces two UUID-named paths and neither container can
   see the other's path.
3. A write inside `/workspace` is visible through the Domus HTTP file API after
   close/fsync.
4. Restarting the HTTP service leaves DOFS mounts intact.
5. Restarting `domus-dofs` restores desired mounts and causes dependent user
   containers to be recreated.
6. Stopping PostgreSQL makes the lease heartbeat mark writable mounts
   unhealthy instead of continuing to accept writes indefinitely.

Common failures:

- `option allow_other only allowed`: enable `user_allow_other` in
  `/etc/fuse.conf` and restart DOFS.
- `unmount_failed` or `busy`: stop the dependent user container, then run
  `dofs unmount` or `dofs reconcile`; do not use lazy unmount.
- `live or foreign mount`: inspect the exact path with `findmnt`; DOFS refuses
  to take ownership automatically.
- readiness `degraded`: inspect `dofs status --json` and the journal before
  restarting the service.
