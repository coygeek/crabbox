# UTM

UTM runs a disposable clone of a prepared, stopped Linux ARM64 QEMU VM or
installs a fresh Apple macOS ARM64 VM from an explicit local IPSW on the local Mac. Crabbox owns the clone and reuses the normal SSH sync, command,
results, retention and release paths. The source template stays outside the
lease's destructive operations.

## Requirements

- A local macOS host with UTM and its bundled `utmctl`. UTM's published scripting
  interface must be accessible from the process running Crabbox. macOS may ask
  for Automation permission to control UTM. Remote UTM hosts are unsupported.
- Configure `utm.storageRoot` to the existing local directory where UTM creates
  cloned `.utm` bundles. Crabbox locates only the exact recorded clone UUID there;
  it does not infer a bundle from its display name or edit UTM registry files.
- A stopped **QEMU Linux ARM64 (`aarch64`)** template registered in UTM. Select
  it by UUID from `utmctl list`, not its display name.
- Exactly one **emulated** network adapter with DHCP enabled in the guest. Extra
  QEMU arguments are rejected because they can override network isolation.
- A prepared guest SSH service listening on port 22, an authorized private key,
  and a user who can write the configured work root. The guest needs `bash`,
  `rsync`, `tar`, `uname` and Linux `/sys/class/net/*/address`.

Lifecycle uses UTM's published AppleScript commands from `osascript`. The bundled
CLI can have a separate macOS Automation authorization status; a working
`utmctl version` is not a lifecycle prerequisite.

A guest agent is not required. QEMU guest-agent IP addresses inside emulated
networking are not host-routable, so Crabbox allocates an explicit loopback port
forward. The prepared-template path supports QEMU Linux ARM64 only. Shared
networking is used by the separate fresh-macOS path below. Bridged networking,
x86 guests, native Windows and desktop/browser/code capabilities are unsupported.
This acquisition feature does not add native checkpoints.

## Configuration

```yaml
provider: utm
target: linux
architecture: arm64
utm:
  appPath: /Applications/UTM.app
  storageRoot: /path/to/utm-bundles
  template: 11111111-1111-4111-8111-111111111111
  user: crabbox
  sshKey: ~/.ssh/prepared-vm
  workRoot: /tmp/crabbox
  startupTimeout: 5m
```

The UUID above is a placeholder. Prepare and shut down your template before
selecting its actual UUID. `utm.sshKey` may fall back to the shared `sshKey`
configuration. The template acquisition path does not install keys, change user preferences,
install guest software, or mutate the template. Credentials are reused from the
prepared template, so restrict access to its private key and guest services.

`storageRoot` is required for acquisition and must point to UTM's actual clone
store, which can differ from the location of an imported template. Choose the
existing directory used by your UTM installation; do not create a separate empty
store. The provider needs local read/write access to owned clone bundles.

All fields have corresponding `--utm-*` flags (`appPath` uses `--utm-app`) and
`CRABBOX_UTM_*` environment variables. Inspect the exact bindings with:

```bash
crabbox providers describe utm --json
crabbox config show --provider utm
crabbox doctor --provider utm
crabbox run --provider utm --keep -- sh -c 'git diff; mkdir -p artifacts; uname -a > artifacts/guest.txt'
crabbox ssh --provider utm LEASE_ID
crabbox stop --provider utm LEASE_ID
```

Normal `crabbox run` syncs the local checkout, including local edits, and uses the
configured results patterns to retrieve artifacts. `--keep` retains the clone
for subsequent commands using its lease ID or slug. Display-name changes in UTM
do not change the recorded UUID or SSH port.

## Identity and recovery

Before cloning, Crabbox durably records an allocation journal beneath its state
directory's `utm/` directory. The record binds a random owned name, source UUID,
pre-clone inventory, lease ID, unique locally administered MAC and loopback port.
The clone command does not return a new UUID. Crabbox therefore requires exactly
one newly created VM with the allocated name, records its UUID before boot, and
uses only that UUID for subsequent operations. An ambiguous or absent discovery
retains the journal and reports its lease ID.

UTM 5.0.8 rejects nested port-forward records through its configuration update
Apple event. Crabbox uses the documented external-bundle editing and `reload
configuration` contract for this step. It requires the exact clone to remain
stopped, locates one regular `.utm` bundle by its on-disk UUID, verifies its
name/backend/architecture against live inventory, and records the bundle path.
It edits a private temporary copy with macOS `plutil`, preserves unrelated plist
values, checks that the original and live configuration did not change, atomically
replaces `config.plist`, and reloads through UTM. Symlink bundles/configs, duplicate
UUIDs, stale identity or concurrent configuration changes are rejected. Neither
Python nor an additional plist library is required.

Crabbox replaces the clone's NIC MAC and all its port forwards with one
`127.0.0.1:PORT -> guest:22` TCP forward. It reads this configuration back before
boot. Cross-process lifecycle locking and the recorded ports/MACs prevent
Crabbox allocations from sharing identities. Another local program can still
claim a released port before UTM starts; authenticated readiness also checks the
new clone MAC over SSH, so a different guest fails acquisition. A running status
or reachable port alone is insufficient.

A failed or interrupted acquisition leaves its journal visible in `crabbox ls
--provider utm`. Release the exact lease with `crabbox stop --provider utm
LEASE_ID`; `crabbox cleanup --provider utm` also retries incomplete allocations
that were not retained with `--keep`, and ordinary expired-lease cleanup.
Pending discovery is reconciled against the recorded inventory before any delete
is allowed. If that identity remains ambiguous, cleanup refuses and keeps the
journal; inspect UTM's inventory before retrying. Do not delete ownership state
manually while a clone might still exist.

Release validates the recorded UUID and configured ownership/network marker,
force-stops only the owned clone, requests deletion, and refreshes inventory to
confirm absence before removing local ownership. Failed stop, deletion or
readback remains visible and retryable. The source and unrelated VMs are never
selected for deletion by name. Local lifecycle expiry needs a running Crabbox
cleanup invocation; UTM does not enforce a remote destruction deadline.

Doctor checks the CLI executable with `--help`, application inventory and stopped-template
configuration, plus access to the configured clone storage directory. It creates
or starts no VM. Guest OS, architecture, authentication,
tools and intended clone identity are verified only after acquisition.


## Fresh macOS from an IPSW

Set `utm.acquisition: ipsw` to create an independent Apple-backend VM without a
prepared template. This mode requires all of the following:

- A local Apple Silicon Mac running macOS 27 or later, with UTM 5.0.8 or later
  and working published application scripting access.
- Full Xcode selected with a macOS 27 or later SDK. Crabbox compiles a small
  embedded Swift helper using `xcrun swiftc`, ad-hoc signs it with Apple's
  virtualization entitlement, and removes its private temporary directory after
  the read-only inspection. This dependency is on the host; the guest does not
  need Xcode, Node.js, or a guest agent for SSH workloads.
- An operator-selected, readable local IPSW containing macOS 27 or later and
  no newer than the host. Native `VZMacOSRestoreImage` inspection must find a
  supported hardware model. Older host/guest combinations require a prepared
  guest rather than automatic Setup Assistant bypass.
- The actual UTM bundle storage directory and enough local capacity for the
  guest installation. A new VM keeps UTM's macOS display/input devices, one
  Shared NIC, and its default 64 GiB system disk. CPU and memory are chosen to
  meet the restore image's native requirements, with a baseline of two CPUs and
  8 GiB memory.

```yaml
provider: utm
target: macos
architecture: arm64
utm:
  acquisition: ipsw
  appPath: /Applications/UTM.app
  storageRoot: /path/to/utm-bundles
  ipsw: /path/to/UniversalMac_Restore.ipsw
  user: crabbox
  workRoot: /tmp/crabbox
  installTimeout: 45m
  startupTimeout: 10m
```

Omit `utm.template`. No prepared-template SSH key or password is required for
this mode. `/tmp/crabbox` is the shared default work root on both guest platforms;
an explicit `utm.workRoot` remains effective. The guest needs its stock `bash`,
`rsync`, `tar`, `sw_vers` and `ifconfig` tools.

Crabbox generates a fresh random account password in memory, validates it and
the account with Apple's SDK before creating a VM, and sends it to the native
validator and UTM first-boot command through protected stdin. It never puts that
password on argv, in configuration, in the ownership journal, or in ordinary
command output. Credential-bearing operation failures report fixed categories;
their raw error output is suppressed. Automatic login stays disabled. First boot
creates the requested account and enables Remote Login through UTM's supported
provisioning record.

The password is used only for initial in-process SSH authentication. The guest
must prove Darwin/ARM64, the inspected macOS version/build, and its recorded NIC
MAC before Crabbox installs a generated per-lease public key. Crabbox then pins
the observed SSH host key and verifies a separate key-only SSH connection before
returning a workload lease. The generated private key stays in the protected
provider state directory until confirmed release. Initial host-key trust relies
on the trusted local VM network; ARP discovery and the guest MAC check do not
provide an independent cryptographic channel to the hypervisor. Continuing
connections require the pinned host key. Restrict access to the host and local
VM network accordingly.

`crabbox doctor --provider utm` checks the selected mode without creating,
installing, or starting a VM. In IPSW mode it reports image compatibility, host
and guest versions, resource requirements and native account validation, and
explains the temporary compiler/helper work it performs. Successful preflight is
not successful OS installation or workload readiness.

Creation, installation and first boot have separate durable phases. The
installation-start response is pending: Crabbox polls `query install` through
completion and observes the owned VM stopped before first boot. UTM checks
pending-install, suspended and recovery eligibility before it starts the guest.
An uncertain first-start submission is recorded before sending credentials and
is never replayed. A UTM restart can lose its in-memory installation query;
Crabbox preserves the recovery record rather than guessing whether it should
reinstall. It never resets an existing guest account.

The Apple backend's published `query ip` reads its first NIC's IPv4 ARP entry.
Crabbox polls this within the startup deadline and verifies the same MAC inside
the authenticated guest. DHCP addresses can change; retained-lease resolution
refreshes discovery and verifies the pinned key and guest identity, without
reinstalling or replaying first-boot credentials. The original IPSW can be moved
after acquisition without blocking retained reuse or release. VM display-name
changes are allowed when the exact journal UUID, ownership marker, bundle and
NIC identity remain consistent.

`crabbox ls --provider utm` exposes separate `utm_installation`,
`utm_first_boot_submitted`, `utm_account_verified` and `utm_ssh_verified` labels.
On failure, preserve the journal and use the normal exact-lease `stop` or cleanup
path. Confirmed release deletes only the owned VM and its derived generated-key
files. The IPSW and unrelated VMs remain outside the destructive scope. A lost
initial account password is not recovered or persisted; continuing access uses
the lease's private key. If acquisition never reaches continuing SSH readiness,
release the incomplete lease and acquire a new VM.
