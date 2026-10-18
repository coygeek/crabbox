# UTM workload acceptance record

The recipe is pinned to UTM v5.0.8 source
`535d6592a8bdc22b48de53703a371b80bf60d587`. Its build and package commands come
from that revision's macOS development guide and scripts. No live compilation,
packaging or guest boot is claimed by this initial record.

| Property | Current result |
| --- | --- |
| Source and preparation procedure | Documented against the pinned release source |
| Helper regression checks | See the delivery check output; rerun the documented unittest command |
| Crabbox Static SSH macOS build | Unverified until a fresh build manifest and run receipt are retained |
| Unsigned packaged app | Unverified until `build=passed` and `package=passed` in the same manifest |
| Physical Mac QEMU HVF guest | Unverified until fresh guest nonce, boot ID and HVF launch arguments are retained |
| QEMU TCG emulation | Untested; requires its own fresh run |
| Apple virtualization | Untested; requires a separate bound SSH/serial challenge |
| Nested hardware virtualization | Untested; host-specific proof required |
| Graphics/renderer workload | Untested; guest boot does not establish graphics behavior |

To replace an unverified row with an observed result, retain the helper's bounded
`evidence/` files and the Crabbox run receipt outside source inputs. Summarize the
observed runner OS/build, architecture, full Xcode identity, source/submodule and
dependency provenance, signing mode, app bundle identity, exact backend and guest
image hash. Name the build, package and runtime run IDs separately. Record failed
or blocked lanes, including Automation context limitations, alongside passing
lanes. Never copy secrets, host identity/account details, signing profiles or
personal VM configuration into this public record.
