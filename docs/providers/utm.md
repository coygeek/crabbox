# UTM

UTM runs a disposable clone of a prepared, stopped Linux ARM64 QEMU VM on the
local Mac. Crabbox owns the clone and reuses the normal SSH sync, command,
results, retention and release paths. The source template stays outside the
lease's destructive operations.

## Requirements

- A local macOS host with UTM and its bundled `utmctl`. UTM's published scripting
  interface must be accessible from the process running Crabbox. macOS may ask
  for Automation permission to control UTM. Remote UTM hosts are unsupported.
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
forward. Shared/bridged networking, Apple backend guests, x86 guests, native
Windows, macOS, desktop/browser/code capabilities and native checkpoints are
not supported by this initial implementation.

## Configuration

```yaml
provider: utm
target: linux
architecture: arm64
utm:
  appPath: /Applications/UTM.app
  template: 11111111-1111-4111-8111-111111111111
  user: crabbox
  sshKey: ~/.ssh/prepared-vm
  workRoot: /tmp/crabbox
  startupTimeout: 5m
```

The UUID above is a placeholder. Prepare and shut down your template before
selecting its actual UUID. `utm.sshKey` may fall back to the shared `sshKey`
configuration. The provider does not install keys, change user preferences,
install guest software, or mutate the template. Credentials are reused from the
prepared template, so restrict access to its private key and guest services.

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
configuration. It creates or starts no VM. Guest OS, architecture, authentication,
tools and intended clone identity are verified only after acquisition.
