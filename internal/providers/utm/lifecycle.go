package utm

import (
	"context"
	"errors"
	"fmt"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

func (b *backend) ReleaseLease(ctx context.Context, req core.ReleaseLeaseRequest) error {
	unlock, err := b.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := b.lookup(req.Lease.LeaseID)
	if err != nil {
		return err
	}
	if req.Lease.Server.Provider != "utm" || req.Lease.Server.CloudID != r.VMID {
		return fmt.Errorf("UTM release target does not match ownership journal")
	}
	if err = core.ValidateLeaseTargetProviderIdentity(req.Lease, req.ExpectedProviderIdentity); err != nil {
		return err
	}
	return b.releaseRecord(ctx, &r, req.CheckpointID, req.GuardedRemoteCleanup)
}
func (b *backend) releaseRecord(ctx context.Context, r *record, checkpointID string, teardown func(context.Context, core.LeaseTarget)) error {
	vms, err := b.inventory(ctx)
	if err != nil {
		return err
	}
	if r.VMID == "" {
		if err = b.reconcile(ctx, r, vms); err != nil {
			return err
		}
	}
	claim, exists, err := core.ReadLeaseClaimWithPresence(r.LeaseID)
	if err != nil {
		return err
	}
	if exists {
		if claim.Provider != "utm" || claim.CloudID != r.VMID || claim.ProviderScope != (Provider{}).ClaimScope(b.cfg) {
			return fmt.Errorf("UTM release claim identity mismatch")
		}
		if err = core.AuthorizeCheckpointRelease(claim, checkpointID); err != nil {
			return err
		}
	}
	action := func() error {
		// Refresh while holding the local claim fence; a cached inventory observation
		// cannot authorize a later deletion.
		current, err := b.inventory(ctx)
		if err != nil {
			return err
		}
		v, present := findVM(current, r.VMID)
		if present {
			if v.ID == r.SourceID {
				return fmt.Errorf("refusing to delete UTM source template")
			}
			if r.Configured {
				if err = validateClone(v, *r); err != nil {
					return err
				}
			}
			if teardown != nil {
				teardown(ctx, b.target(*r, current))
			}
			r.State = "deleting"
			r.Labels["state"] = "deleting"
			if err = saveRecord(*r); err != nil {
				return err
			}
			if v.Status != "stopped" {
				if err = b.control(ctx, "stop", r.VMID); err != nil {
					return err
				}
				if err = b.waitObservation(ctx, r.VMID, false); err != nil {
					return err
				}
			}
			deleteErr := b.control(ctx, "delete", r.VMID)
			absenceErr := b.waitObservation(ctx, r.VMID, true)
			if absenceErr != nil {
				return errors.Join(deleteErr, absenceErr)
			}
		}
		return nil
	}
	if err = core.CleanupLeaseClaimIfUnchangedAfterContext(ctx, r.LeaseID, claim, exists, action); err != nil {
		return err
	}
	return removeRecord(r.LeaseID)
}
func (b *backend) waitObservation(ctx context.Context, id string, absent bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var last error
	for {
		vms, err := b.inventory(ctx)
		if err == nil {
			v, ok := findVM(vms, id)
			if absent && !ok || !absent && ok && v.Status == "stopped" {
				return nil
			}
			last = fmt.Errorf("UTM UUID %s status=%s present=%t", id, v.Status, ok)
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("UTM disposition not confirmed (journal retained): %w", errors.Join(last, ctx.Err()))
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (b *backend) Cleanup(ctx context.Context, req core.CleanupRequest) error {
	unlock, err := b.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	records, err := b.records()
	if err != nil {
		return err
	}
	vms, err := b.inventory(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, r := range records {
		s := b.project(r, vms)
		eligible, reason := core.ShouldCleanupServer(s, core.ClockNow(b.rt.Clock).UTC())
		if r.State != "ready" && !r.Keep {
			eligible = true
			reason = "incomplete allocation"
		}
		if !eligible {
			fmt.Fprintf(b.rt.Stderr, "skip UTM lease=%s reason=%s\n", r.LeaseID, reason)
			continue
		}
		if req.DryRun {
			fmt.Fprintf(b.rt.Stdout, "would remove UTM lease=%s vm=%s reason=%s\n", r.LeaseID, core.Blank(r.VMID, "unresolved"), reason)
			continue
		}
		if err = b.releaseRecord(ctx, &r, "", nil); err != nil {
			failures = append(failures, fmt.Errorf("UTM lease=%s: %w", r.LeaseID, err))
			continue
		}
		fmt.Fprintf(b.rt.Stdout, "removed UTM lease=%s vm=%s\n", r.LeaseID, r.VMID)
	}
	return errors.Join(failures...)
}
func (b *backend) Doctor(ctx context.Context, _ core.DoctorRequest) (core.DoctorResult, error) {
	result := core.DoctorResult{Provider: "utm", Message: "local macOS + prepared Linux QEMU template; emulated network + unique loopback SSH; mutation=false", Checks: []core.DoctorCheck{}}
	add := func(name, status, message string) {
		result.Checks = append(result.Checks, core.DoctorCheck{Check: name, Status: status, Message: message})
	}
	_, err := b.command(ctx, b.cfg.UTM.AppPath+"/Contents/MacOS/utmctl", []string{"--help"}, nil)
	if err != nil {
		add("cli", "error", err.Error())
		result.Status = "error"
	} else {
		add("cli", "ok", "bundled utmctl executable is available; application control is checked separately")
	}
	vms, err := b.inventory(ctx)
	if err != nil {
		add("application-control", "error", err.Error())
		result.Status = "error"
		return result, nil
	}
	add("application-control", "ok", "published UTM scripting inventory is readable; no VM created or started")
	template, ok := findVM(vms, b.cfg.UTM.Template)
	if !ok {
		add("template", "error", "set utm.template to the UUID of a stopped prepared Linux QEMU VM")
		result.Status = "error"
	} else if err = validateTemplate(template); err != nil {
		add("template", "error", err.Error())
		result.Status = "error"
	} else {
		add("template", "ok", "stopped QEMU template with one emulated NIC; guest OS and tools remain unverified until authenticated acquisition")
	}
	add("guest-readiness", "unchecked", "acquisition must verify authorized SSH, Linux architecture, clone MAC, bash, rsync and tar; UTM running status or guest-agent IP alone is insufficient")
	return result, nil
}
