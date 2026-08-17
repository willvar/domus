# Workspace production deployment

This guide deploys the Linux-only, single-host Workspace Manager alongside
DOFS and the Domus web control plane. Read [workspace architecture](workspace.md)
and [DOFS production deployment](dofs-production.md) first.

## Supported production shape

The current supported shape is one Linux host with rootful Docker Engine and
host-visible FUSE mounts. Required host components are:

- a Linux kernel and `/dev/fuse`;
- FUSE 3 with `fusermount3`;
- `/etc/fuse.conf` containing an uncommented `user_allow_other`;
- rootful Docker Engine with a local Unix socket;
- PostgreSQL and the configured S3-compatible object store;
- an encrypted local volume for `dofs.state_root`;
- systemd for the supplied service definitions.

Remote Docker TCP endpoints are rejected. Rootless Docker is not currently a
supported production topology because its daemon mount namespace and UID
mapping may not see the host DOFS mounts consistently. Kubernetes/multi-host
scheduling also needs a separate fencing and drain design for host-local DOFS
WAL.

## Build and pin the image

Build the curated image before starting the manager:

```bash
make workspace-image
make test-workspace-docker
docker image inspect domus-workspace:0.1.0 --format '{{.Id}}'
```

The Dockerfile pins the Debian 13 slim base manifest and installs Bash, OpenSSH
client, FFmpeg, ImageMagick, Poppler, and the fixed preview/transcode scripts.
The real integration test validates non-root bind read/write, raw TTY, image
and PDF preview, audio transcode, dynamic Unix/OpenSSH home resolution, exact
mount shape, and the principal hardening and resource settings.

Debian slim is the curated default, not a protocol requirement. Operators may
publish another reviewed image and set `workspace.image`, provided it preserves
the non-root UID/GID contract, `/bin/sh`, `/bin/bash`, `/workspace`, the two
Domus media scripts and every executable those scripts invoke. Image choice is
deployment policy and is never controlled by an end user.

| Base | Best fit | Main tradeoff |
| --- | --- | --- |
| Debian stable slim | Default general-purpose terminal and media runtime | Larger than Alpine, but uses glibc and the broadly supported APT ecosystem. |
| Ubuntu LTS | Sites whose tooling and vendor packages explicitly target Ubuntu | Usually larger and more opinionated than Debian slim. |
| Alpine | Extremely storage-sensitive, tightly curated appliances | Smallest image, but musl and BusyBox create more compatibility exceptions for arbitrary Unix tooling. |
| Distroless/scratch | Single non-interactive application workers | Not suitable for the reusable interactive shell contract. |

The container remains non-root with a read-only image root, so selecting Debian
does not let terminal users run `apt install`. Additional packages must be baked
into a reviewed derivative image; a future site can offer separate curated
`base` and `developer` images without weakening the runtime boundary.

For a registry deployment, build, scan, sign, and publish the image in CI,
then configure an immutable registry digest such as
`registry.example/domus-workspace@sha256:...` with `pull_policy: never` after
preloading it on the host. A mutable tag with `always` is convenient but makes
an unreviewed registry change an execution-plane rollout. The manager hashes
the resolved image ID; an image change recreates containers instead of mixing
old and new roots. Before announcing readiness, startup resolves this image;
with `pull_policy: never`, a missing preloaded image therefore fails the daemon
instead of surprising the first terminal or media job.

## Accounts, groups, and files

Install the supplied account declaration and units:

```text
deploy/systemd/domus.sysusers.conf       -> /usr/lib/sysusers.d/domus.conf
deploy/systemd/domus-dofs.service        -> /etc/systemd/system/domus-dofs.service
deploy/systemd/domus-workspace.service   -> /etc/systemd/system/domus-workspace.service
deploy/systemd/domus.service             -> /etc/systemd/system/domus.service
deploy/workspace.example.yaml            -> /etc/domus/workspace.yaml
```

Run `systemd-sysusers`, then verify the effective memberships. The supplied
layout uses:

| Resource | Owner/access |
| --- | --- |
| `/etc/domus/config.yaml` | `domus:domus`, mode `0600` |
| `/etc/domus/workspace.yaml` | `domus-workspace:domus-workspace`, mode `0600` |
| `/run/domus/dofs.sock` | `domus:domus`, mode `0660` |
| `/run/domus-workspace/control.sock` | `domus-workspace:domus`, mode `0660` |
| `/var/lib/domus` | `domus`, mode `0700` |
| `/var/lib/domus-workspace` | `domus-workspace`, mode `0700` |
| Docker Unix socket | group `docker` (distribution managed) |

The workspace account is intentionally a member of `docker`. Access to that
socket is effectively host-root authority. Do not add the web account or any
user container to the Docker group, and do not copy the Docker socket into a
container. If the distribution uses another Docker group/socket path, change
`SupplementaryGroups` and `workspace.docker_host` together.

The separate workspace configuration must remain minimal. It needs no
database, OSS, SMTP, session, server-encryption, or user key material. Avoid
adding those sections merely for convenience.

## Configuration

In the main `/etc/domus/config.yaml`, make the DOFS socket accessible to the
workspace account and configure the required workspace control socket:

```yaml
dofs:
  mount_root: /var/lib/domus/dofs/mounts
  state_root: /var/lib/domus/dofs/state
  control_socket: /run/domus/dofs.sock
  socket_group: domus
  uid: 1000
  gid: 1000
  allow_other: true
  writable: true
  max_mounts: 32

workspace:
  control_socket: /run/domus-workspace/control.sock
```

The shown client fields use the same production defaults as
`workspace.example.yaml`. If you override `max_sessions_per_user`,
`operation_timeout_seconds`, `exec_timeout_seconds`, or
`shutdown_timeout_seconds` in the daemon file, set the same values in this
main `workspace` section. The web process consumes those four admission and
deadline settings but still receives no Docker host, image, mount path, or
runtime credentials.

Use `deploy/workspace.example.yaml` for `/etc/domus/workspace.yaml`. Important
settings are:

- `dofs_mount_root` must exactly equal the DOFS manager's `mount_root`;
- workspace `uid`/`gid` must exactly equal DOFS's synthetic UID/GID and must
  be non-root;
- `max_running` should not exceed `dofs.max_mounts`;
- `memory_swap_bytes` must be at least `memory_bytes`; setting them equal
  prevents swap from expanding the effective memory ceiling;
- `max_sessions_per_user` covers interactive TTYs and non-interactive Exec;
- `exec_timeout_seconds` is also the upper bound used by preview/transcode;
- `exec_output_limit_bytes` has a hard 16 MiB ceiling so base64-encoded control
  responses remain within the bounded 32 MiB protocol frame;
- `network_mode: none` is the safest setting, while `bridge` is required for
  ordinary outbound SSH/HTTP access unless a named restricted network is used;
- `read_only_rootfs` is a mandatory invariant and must remain `true`; writable
  state is limited to `/workspace` plus bounded tmpfs mounts;
- `pull_policy: never` prevents runtime registry access and fails if the image
  was not deliberately loaded.

Both configuration validators reject root paths, overlapping workspace state
and DOFS mount trees, host/container network namespace reuse, root container
identity, writable container roots, unlimited resource values, remote Docker
endpoints, and unsafe runtime names.

Before starting an upgraded instance, verify every existing username matches
`[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}`. The manager rejects any other value
because it cannot safely generate passwd/group records and home paths for it.
The admin API rejects newly-created incompatible usernames with
`workspace_incompatible_username`.

## systemd ordering and mount visibility

The required startup sequence is:

```text
network/PostgreSQL/Docker
  -> domus-dofs.service
    -> domus-workspace.service
      -> domus.service
```

The workspace unit declares hard dependencies on DOFS and Docker and reports
`READY=1` only after both dependencies, the configured image, manager locks,
and its control socket are live. The supplied `domus.service` requires and starts
after `domus-workspace.service`; the web process independently performs a
ready/protocol-version check and fails closed during startup when the daemon
or its dependencies are unavailable. A later manager restart does not take
down unrelated HTTP file operations: workspace calls return unavailable until
the local socket is ready again. A `degraded`
response caused by an individual desired user is logged but does not block the
web process; readiness monitoring remains non-OK so operators still see it.

DOFS must run in the host mount namespace so dockerd can resolve the bind
source. Do not add filesystem-namespace hardening directives to the DOFS unit;
the comments in that unit list the unsafe directives. The workspace daemon may
use `PrivateMounts=yes` because it sends the exact host path to dockerd and
never traverses plaintext itself.

After installation:

```bash
systemd-analyze verify /etc/systemd/system/domus-dofs.service \
  /etc/systemd/system/domus-workspace.service \
  /etc/systemd/system/domus.service
systemctl daemon-reload
systemctl enable --now docker domus-dofs domus-workspace domus
```

Install and verify the image, DOFS socket permissions, and Workspace Manager
before starting Domus Web. There is no configuration switch or legacy
execution fallback.

## Health and operator commands

The control protocol is HTTP over a Unix socket; socket ownership is its
authentication. It provides live/ready health, list/status, ensure, stop,
remove, bounded exec, raw TTY, and reconciliation operations. Use the CLI
instead of calling it directly:

```bash
domus workspace health -c /etc/domus/workspace.yaml --ready
domus workspace list -c /etc/domus/workspace.yaml --json
domus workspace ensure -c /etc/domus/workspace.yaml \
  --user-id <immutable-user-id> --user <username>
domus workspace exec -c /etc/domus/workspace.yaml \
  --user-id <immutable-user-id> --user <username> -- id
domus workspace stop -c /etc/domus/workspace.yaml --user-id <immutable-user-id>
domus workspace reconcile -c /etc/domus/workspace.yaml
```

`health/live` proves the manager process and socket handler are responsive.
`health/ready` also pings Docker and DOFS and reports degraded desired
workspaces. Capacity-exhausted users remain `pending`; the manager never
silently exceeds `max_running`.

Monitor at least:

- live and ready health separately;
- desired/running/degraded workspace counts and active exec count;
- Docker OOM kills, restarts, image ID, and per-container resource use;
- DOFS mount health, lease loss, WAL/state-volume free space, and database
  connection headroom;
- repeated container recycle caused by command cancellation/output failure;
- workspace and DOFS journal errors during user deletion or idle teardown.

## Lifecycle and recovery

Lifecycle operations are serialized per user; the final global capacity slot
is also serialized across users. The manager holds an exclusive state-root
lock, persists a UUID manager identity, and modifies only containers with that
exact identity label. Foreign same-name containers block startup for the user
and are never stopped or deleted.

Teardown order is always:

```text
cancel/close active work
  -> stop and remove exact owned container
    -> normal DOFS unmount
      -> remove desired workspace marker
```

If Docker or unmount fails, the desired marker is retained so reconciliation
can retry instead of leaking an untracked plaintext mount. User-account
deletion follows the same order and aborts before database/OSS deletion if the
workspace or DOFS cannot be torn down safely.

DOFS applies the same rule independently: its durable desired marker is
committed away only after the real FUSE unmount and mountpoint cleanup succeed.
Thus a Workspace Manager crash, a DOFS crash, or a busy mount leaves a durable
recovery intent rather than an unowned mount.

Normal service shutdown preserves markers, stops all active work, waits for
bounded operations, removes containers, and unmounts DOFS. A subsequent start
reconciles desired users. Never use lazy FUSE unmount to force an upgrade.

The web process stops accepting HTTP requests first, closes browser terminal
connections in parallel, and then cancels and drains preview/transcode jobs.
HTTP drain has its own 10-second bound; media drain has an independent bound of
at least 45 seconds (or `workspace.shutdown_timeout_seconds`, whichever is
larger), so terminal cleanup cannot consume the media cleanup window. The
workspace and DOFS systemd units stop afterward in reverse dependency order.

To roll out a new workspace image or security/resource specification, update
the minimal workspace config and restart `domus-workspace.service` during a
drain window. Active terminals are disconnected. The new resolved image/spec
hash causes clean container recreation; user files and shell/SSH state remain
in DOFS.

Workspace manager state contains labels, activity markers, and generated
passwd/group identity files, not file plaintext or secrets. Identity files are
mounted read-only and removed on an explicit workspace/account removal. DOFS
`state_root`, however, contains plaintext sparse cache/WAL and must stay on its
protected encrypted volume. Do not back up live container writable layers as
user data: the root is read-only and `/tmp` and `/run` are intentionally
ephemeral.

## Release verification

Run the automated suite before enabling production traffic:

```bash
go test ./... -count=1 -timeout 120s
go test -race ./internal/workspace ./internal/terminal ./internal/handler -count=1
go vet ./...
make workspace-image
make test-workspace-docker
cd frontend && npm run check && npm run build
```

On a host with real FUSE and `user_allow_other`, also run:

```bash
DOFS_FUSE_INTEGRATION=1 \
go test ./internal/dofs -run 'Test(FUSEMount|ManagerOwnsRealFUSE)' -v -count=1

DOFS_DOCKER_INTEGRATION=1 DOFS_DOCKER_IMAGE=domus-workspace:0.1.0 \
go test ./internal/dofs -run TestFUSEBindMountIntoDocker -v -count=1
```

Then complete this end-to-end checklist with two non-root users:

1. Open both browser terminals and verify each has UID 1000, starts in
   `/workspace/home/<username>`, and cannot see the other user's files.
2. Create, edit, rename, and delete a file in the terminal; verify the HTTP
   file API and a fresh browser session observe the encrypted DOFS result.
3. Generate an SSH key, reconnect the terminal, and verify `.ssh` persists;
   connect outbound and verify host-key checking behaves normally.
4. Upload an image, video, and PDF without browser thumbnails; verify preview
   tasks complete and originals remain usable if a preview is forced to fail.
   Overwrite and delete a previewed source and verify its hidden thumbnail row
   and object are reclaimed after open handles drain.
5. Request both transcode profiles, observe task progress/cancellation, and
   verify outputs appear as normal encrypted files.
6. Keep a terminal open beyond the idle timeout, close it, and verify the
   container is reaped only after a fresh full idle interval.
7. Restart DOFS and verify the changed `mount_id` causes container recreation,
   not reuse of a disconnected bind.
8. Restart the web process alone and verify the DOFS/workspace services remain
   healthy; restart the workspace service in a drain window and verify desired
   workspaces recover.
9. Exhaust `max_running` and per-user sessions and verify requests become
   explicit `pending`/capacity errors without exceeding configured limits.
10. Delete a test user and verify its terminal closes, container disappears,
    DOFS unmounts, and only then account data is removed.

Before and after testing, the following query should show only expected live
workspaces and no integration-test leftovers:

```bash
docker ps -a --filter label=io.domus.workspace.managed=true
```

## Known operational boundaries

- Linux, FUSE, and rootful Docker are required for the execution plane.
- A cancelled or timed-out non-interactive Exec recycles the whole user's
  container because Docker cannot signal one exec through its API; concurrent
  terminals for that user disconnect and reconnect on next use.
- Containers share the host kernel. Fully hostile multi-tenant execution needs
  an additional sandbox/VM boundary.
- There is no GPU/device passthrough, inbound SSH daemon, or persistent package
  installation in the read-only image.
- DOFS local plaintext-cache quotas and automatic multi-host failover are not
  part of this iteration.
