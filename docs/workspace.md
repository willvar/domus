# User workspace execution plane

Domus gives every user a reusable Linux environment without moving file
encryption keys or OSS credentials into that environment. This is the only
supported execution architecture and requires Linux, FUSE, and Docker.

## Architecture

```text
browser terminal / authenticated HTTP request
  -> Domus control plane
    -> /run/domus-workspace/control.sock
      -> domus workspace serve
        -> Docker Engine Unix socket
        -> /run/domus/dofs.sock
          -> one DOFS FUSE mount per immutable user ID
            -> encrypted OSS objects

user container
  -> exact bind: /var/lib/domus/dofs/mounts/<user-id> -> /workspace
  -> read-only generated identity files -> /etc/passwd and /etc/group
```

The three trust domains are intentionally separate:

- DOFS owns database/OSS access, user KEKs, plaintext writeback state, and the
  FUSE mount. It never calls Docker.
- The Workspace Manager owns the root-equivalent Docker socket, but its minimal
  configuration contains no database, OSS, session, or encryption secrets.
- The web process authenticates the user and calls only the permission-protected
  workspace control socket. It never receives the Docker socket.

User containers receive none of those control sockets, `/dev/fuse`, host keys,
or service credentials. Their only writable host bind is the exact plaintext
DOFS mount at `/workspace`; generated passwd/group files contain only that
user's public name and configured numeric identity.

## One reusable container per user

The first terminal or server-owned media job calls `Ensure` with the immutable
user ID and current username. The manager asks DOFS to resolve that user
independently and rejects any disagreement in user ID, username, UID/GID,
`allow_other`, mount path, or mount health. It then creates or starts one
container named from the configured prefix and user ID.

The manager atomically generates per-user `/etc/passwd` and `/etc/group`
files and mounts them read-only. The process still runs as the configured
non-root numeric UID/GID, while libc, Bash, `whoami`, and OpenSSH consistently
resolve the authenticated username and `/workspace/home/<username>` home.

Container labels record:

- manager identity;
- user ID and username;
- DOFS `mount_id`;
- a hash of the resolved image and security/resource specification.

A matching running container is reused. A changed `mount_id`, image ID, or
container specification causes a stop/remove/recreate cycle before another
command is admitted. This is required because a private Docker bind can retain
a disconnected old FUSE mount even after DOFS remounts the same host path.
A same-name container without the exact ownership labels is treated as a
conflict and is never modified.

Desired workspaces and last-use times are fsync'd below
`workspace.state_root`. Reconciliation restores them after a manager crash,
removes containers orphaned by an interrupted teardown or prefix change, and
reaps a container only after it has no active sessions for the configured idle
timeout. Normal manager shutdown stops containers and unmounts DOFS but keeps
desired markers so restart can restore them.

## Terminal and SSH

`session.open` creates a real raw TTY in the user container. The browser
forwards bytes, resize events, control characters, shell history, pipes,
redirection, completion, and job control to `/bin/bash`.

A normal shell exit leaves the container reusable. If a client disconnects
while its exec process ignores terminal EOF, Docker cannot signal only that
exec; the runtime therefore stops the user's container fail-closed. The next
operation restarts the same managed container, while any concurrent sessions
in that container receive a disconnect.

SSH is the ordinary OpenSSH client installed in the workspace image:

```bash
ssh user@example.com
```

Keys, `known_hosts`, and SSH config live under
`/workspace/home/<username>/.ssh`, so DOFS encrypts and persists them. Outbound
SSH requires `workspace.network_mode` to allow the destination; `none` disables
all container networking. No inbound SSH daemon or host port is exposed.

There is no VSH or process-local SSH fallback. Domus Web fails closed during
startup if Workspace Manager is unavailable or its protocol version differs.
A daemon that is live but reports one historical user as degraded does not
take the whole web control plane offline; reconciliation and operator access
remain available.

## Unified execution

The internal `workspace.Service` has two execution forms:

- `Exec`: bounded, non-interactive argv execution for trusted server features;
- `OpenSession`: a raw interactive TTY stream for the browser terminal.

`Exec` does not invoke a host shell. It validates argv, limits stdin and output,
restricts the working directory to `/workspace`, overlays a small environment,
enforces the configured timeout, and counts against the per-user session cap.
The Unix control protocol is versioned and never exposed on TCP.

The public HTTP API does not accept arbitrary `Exec` commands. Transcoding
offers only fixed profiles and preview generation invokes only curated image
scripts. This keeps authenticated file APIs from becoming a second remote
shell surface.

Docker has no API to signal an individual non-interactive exec process. If an
`Exec` request times out, is cancelled, exceeds its shared stdout/stderr
budget, loses its client, or fails while
streaming stdin, the manager stops that user's whole container to guarantee
that no untracked process remains. Reconciliation restarts it on the next use,
but other terminals in that same container are disconnected. This fail-closed
recycle is the current cancellation boundary and should be visible in user and
operator telemetry.

## Preview and transcode reuse

Browser-generated encrypted thumbnails remain the fast path. If an uploaded
image, video, or PDF has no browser thumbnail, Domus queues a preview task in
the same reusable user container:

```text
original through /workspace
  -> /usr/local/bin/domus-preview
  -> hidden .user/derived JPEG through /workspace
  -> DOFS encrypts and publishes a new generation
  -> source receives metadata only if its generation is unchanged
```

The final metadata update is generation-guarded. If the source is overwritten,
moved, deleted, or otherwise changed while previewing, Domus removes the stale
derived output and fails that preview task instead of attaching a thumbnail for
old content to the new file. Once attached, the hidden thumbnail inode is owned
by that source record. Replacing or deleting the source transactionally
tombstones the thumbnail too; DOFS reclaims its encrypted generations only
after any open thumbnail handle drains. Browser-uploaded thumbnails follow the
same lifecycle, and directory copies rebind to an independent copied thumbnail
instead of sharing the original object.

`POST /file/transcode` accepts only `video-720p` or `audio-mp3`. The fixed
`domus-transcode` script reads and writes through `/workspace`; DOFS performs
encryption and database publication. Transcodes write to a hidden,
task-specific partial path and use a no-clobber rename only after FFmpeg
succeeds, so a user-created destination is not overwritten and incomplete
output is not published in the normal directory. Per-user media admission
reserves at least one configured workspace session for an interactive terminal
and caps media jobs at two. Cancelling a task cancels its execution and removes
a partial derived output where possible.

Preview failure never changes the ready original file, so direct client-side
preview/download remains the fallback.

## Container boundary

Every managed container is created with:

- non-root configured UID/GID;
- all Linux capabilities dropped and `no-new-privileges` enabled;
- Docker's default seccomp/AppArmor policy when available;
- read-only image root, private size-limited `/tmp` and `/run` tmpfs mounts;
- memory plus equal memory+swap ceiling, CPU, PID, shared-memory, and output
  limits;
- no privileged mode, devices, published ports, host PID/IPC namespace, host
  network, Docker socket, or control sockets;
- exactly one `rprivate` writable data bind at `/workspace`, plus read-only
  manager-owned `/etc/passwd` and `/etc/group` identity-file binds.

This is a strong process and filesystem boundary for cooperative users, not a
VM security boundary against hostile kernel exploits. Deployments executing
fully untrusted code should add a hardened runtime such as gVisor/Kata or a VM
boundary, a site-specific seccomp/AppArmor profile, egress controls, image
signing/scanning, and host patch management.

See [production deployment](workspace-production.md) for exact configuration,
service ordering, operations, and the release test checklist. See [DOFS](dofs.md)
for encryption, namespace, writeback, and recovery semantics.
