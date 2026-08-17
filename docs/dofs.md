# DOFS

DOFS (Domus Object File System) exposes one Domus user's encrypted OSS
namespace as a plaintext Linux FUSE mount. Mounts are read-only by default;
`--writable` enables transactional namespace changes and generation-based
writeback.

## Data paths

```text
read(2)
  -> Linux FUSE
  -> files row constrained by user_id and username prefix
  -> current object_key (legacy rows fall back to path)
  -> OSS ciphertext Range GET
  -> AES-GCM chunk-range decryption
  -> plaintext returned to the kernel
```

```text
write/truncate
  -> private local sparse plaintext cache (dirty/read ranges only)
  -> cache fsync
  -> WAL prepared
  -> one sequential base-generation decrypt stream
  -> overlay dirty sparse-cache ranges
  -> stream-encrypt immutable OSS generation
  -> WAL uploaded
  -> files.generation compare-and-swap
  -> WAL removal
```

The worker holds only one `user_id`, username namespace, and user KEK. The
Workspace Manager therefore gives a user container only the plaintext bind
mount, not an OSS credential or encryption key.

`files.id` is the stable inode. Existing records need no object migration: an
empty `object_key` resolves to the legacy logical `path`. New generations live
under `.dofs/objects/<user-id>/<inode-id>/`, outside browser-resolvable user
paths.

## Usage

Linux requires `/dev/fuse` and `fusermount3`. A read-only mount uses:

```bash
mkdir -p /mnt/dofs-root
domus dofs mount \
  -c config.yaml \
  --user root \
  --mountpoint /mnt/dofs-root
```

Enable writable DOFS with a persistent, host-private state directory:

```bash
domus dofs mount \
  -c config.yaml \
  --user root \
  --mountpoint /mnt/dofs-root \
  --writable \
  --state-dir /var/lib/domus/dofs/root
```

When `--state-dir` is omitted, DOFS uses the current account's user cache
directory. It contains transient **plaintext** cache files and is forced to
mode `0700`; production should put it on an encrypted local volume. DOFS locks
the directory. A PostgreSQL advisory lease also rejects any simultaneous
writable mount for the same user, including one on another host or using a
different state directory.

The command stays in the foreground. Send `SIGINT`/`SIGTERM`, or unmount the
filesystem externally, to stop it. `--debug` enables FUSE protocol logging.
For a container running under another host UID, set `--uid`/`--gid` and, only
when needed, `--allow-other`; that also requires `user_allow_other` in
`/etc/fuse.conf`.

## Managed production service

Production should use the multi-user manager instead of launching one manual
process per mount:

```bash
domus dofs serve -c /etc/domus/config.yaml
```

The foreground service owns a permission-protected Unix socket, persists
desired mounts, coordinates concurrent requests, restores mounts after a
restart, reports liveness/readiness, and rejects live or foreign mountpoints
rather than stealing them. It uses immutable user IDs for host paths:

```text
/var/lib/domus/dofs/mounts/<user-id>
```

See [DOFS production deployment](dofs-production.md) for systemd mount-
namespace requirements, Docker handoff, host permissions, recovery semantics,
monitoring, and the current single-host failover boundary.

## Supported operations

- Directory lookup and deterministic listing.
- Plaintext `stat`, buffered reads, and random reads over encrypted OSS ranges.
- Read-only-by-default mounts.
- Opt-in create, mkdir, metadata-only rename, unlink, and empty-directory
  removal.
- Random write, truncate, flush, and fsync with demand-filled sparse cache
  ranges; opening or patching a file does not first download the whole file.
- Immutable encrypted generations with database CAS conflict detection.
- A single sequential base-object download during partial-file publication;
  full overwrites skip the old object entirely.
- Unix open-unlink behavior: namespace removal tombstones the stable inode,
  and physical objects are reclaimed after its final open handle closes.
- Recovery of transactions that reached WAL `prepared`; non-fsynced dirty
  transactions are discarded.
- Per-user logical and physical namespace validation.
- Best-effort removal of in-memory KEK/DEK bytes on unmount.

## Current limitations

- Unix ownership/mode mutation, symlinks, hard links, special nodes, and file
  locking are not implemented.
- Existing metadata has no Unix mode/UID/GID/symlink fields, so DOFS
  synthesizes owner-only permissions.
- Publishing a new immutable generation still has to stream the complete
  logical plaintext once, merging a sequential base decrypt stream with dirty
  cache ranges;
  it does not allocate a complete local plaintext copy and clears cached
  blocks after a successful commit.
- Docker lifecycle, image policy, resource limits, and SSH routing remain
  outside DOFS; `domus workspace serve` consumes DOFS's stable,
  health-checked bind-mount handoff.
- Namespace mutations made concurrently through the HTTP file API do not
  provide distributed Unix open-handle tracking. Generation CAS protects file
  publication, but operators should avoid cross-interface hard deletes while
  a file is open in FUSE until those paths are unified on tombstones.
- Legacy path-based objects can still be overwritten by older clients. Once a
  file publishes an immutable `object_key`, DOFS opens are generation-pinned.
- Historical databases may contain both `name` and `name/` in one parent.
  DOFS preserves the rows and follows legacy lookup behavior by exposing the
  file first; new DOFS namespace operations serialize that name and reject a
  second cross-type entry.
- Superseded generations remain available for generation-pinned readers. Inode
  deletion reclaims its full generation prefix; periodic collection of old
  generations for long-lived files is still a separate maintenance job.

## Verification

```bash
go test ./internal/dofs
```

The real kernel tests are opt-in because many CI environments lack FUSE:

```bash
DOFS_FUSE_INTEGRATION=1 \
go test ./internal/dofs -run 'Test(FUSEMount|ManagerOwnsRealFUSE)' -v
```

The Docker handoff test additionally requires an already-present container
image and a host configured for `allow_other`:

```bash
DOFS_DOCKER_INTEGRATION=1 \
DOFS_DOCKER_IMAGE=domus-workspace:0.1.0 \
go test ./internal/dofs -run TestFUSEBindMountIntoDocker -v
```

## Crash and conflict semantics

- New files remain hidden as `creating` until their encrypted empty generation
  exists; mount recovery removes abandoned creating rows and object prefixes.
- `rename` is a single database transaction. Legacy path-backed objects are
  pinned to an explicit physical key before their logical path changes.
- `fsync` advances WAL state to `prepared`; recovery reconstructs unchanged
  byte ranges from the pinned base generation and replays the upload.
- The per-file generation CAS returns `ESTALE` to a losing concurrent writer.
- A losing transaction first persists a `discarded` WAL state, so recovery can
  only remove its object and can never publish it later.
- Once unlink/replace metadata commits, delayed OSS cleanup errors do not turn
  the syscall into a false failure; the retained tombstone is retried during
  mount recovery.
