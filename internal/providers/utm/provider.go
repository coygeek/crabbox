// Package utm manages disposable clones of prepared local UTM QEMU guests.
package utm

import (
	"flag"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	core "github.com/openclaw/crabbox/internal/cli"
)

func init() { core.RegisterProvider(Provider{}) }

type Provider struct{}

func (Provider) Spec() core.ProviderSpec {
	return core.ProviderSpec{Name: "utm", Family: "utm", Kind: core.ProviderKindSSHLease,
		Authentication: core.DirectProviderAuthentication(core.ProviderAuthenticationLocalContext),
		Targets:        []core.TargetSpec{{OS: core.TargetLinux}}, Features: core.FeatureSet{core.FeatureSSH, core.FeatureCrabboxSync, core.FeatureCleanup}, Coordinator: core.CoordinatorNever, ClassDisposition: core.ProviderClassDispositionUnmapped, ActionsRunnerUnsupported: true}
}
func (Provider) RegisterFlags(fs *flag.FlagSet, cfg core.Config) any {
	return core.RegisterUTMConfigFlags(fs, cfg.UTM)
}
func (Provider) ApplyFlags(cfg *core.Config, fs *flag.FlagSet, values any) error {
	v, ok := values.(core.UTMConfigFlagValues)
	if !ok {
		return nil
	}
	applied, err := v.Apply(&cfg.UTM, fs)
	core.RecordProviderFlagInputs(cfg, applied.InputAccepted, "utm")
	return err
}
func (Provider) ApplyConfigDefaults(cfg *core.Config) error {
	if !core.IsArchitectureExplicit(*cfg) {
		cfg.Architecture = "arm64"
	}
	if cfg.TargetOS == "" {
		cfg.TargetOS = core.TargetLinux
	}
	if cfg.UTM.User != "" {
		cfg.SSHUser = cfg.UTM.User
	}
	if cfg.UTM.WorkRoot != "" {
		cfg.WorkRoot = cfg.UTM.WorkRoot
	}
	return nil
}
func (Provider) SupportsArchitecture(cfg core.Config, arch string) bool {
	return cfg.TargetOS == core.TargetLinux && arch == "arm64"
}
func (Provider) ClaimScope(cfg core.Config) string {
	return "utm:" + filepath.Clean(core.ExpandUserPath(cfg.UTM.AppPath))
}
func (p Provider) Configure(cfg core.Config, rt core.Runtime) (core.Backend, error) {
	if cfg.TargetOS != "" && cfg.TargetOS != core.TargetLinux {
		return nil, core.Exit(2, "provider=utm currently supports prepared Linux QEMU guests only; Apple, macOS and Windows guests are unsupported")
	}
	if cfg.Tailscale.Enabled || string(cfg.Network) == "tailscale" {
		return nil, core.Exit(2, "provider=utm requires the local loopback SSH transport; Tailscale is unsupported")
	}
	if cfg.Desktop || cfg.Browser || cfg.Code {
		return nil, core.Exit(2, "provider=utm does not support desktop, browser or code capabilities")
	}
	c := cfg.UTM
	if c.AppPath == "" {
		c.AppPath = core.UTMConfigDefaultAppPath
	}
	c.AppPath = core.ExpandUserPath(c.AppPath)
	if !filepath.IsAbs(c.AppPath) || filepath.Ext(c.AppPath) != ".app" {
		return nil, fmt.Errorf("utm.appPath must be an absolute .app bundle path")
	}
	if c.User == "" {
		c.User = core.UTMConfigDefaultUser
	}
	if c.WorkRoot == "" {
		c.WorkRoot = core.UTMConfigDefaultWorkRoot
	}
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = core.UTMConfigDefaultStartupTimeout
	}
	c.SSHKey = core.ExpandUserPath(c.SSHKey)
	if c.SSHKey == "" {
		c.SSHKey = core.ExpandUserPath(cfg.SSHKey)
	}
	if strings.ContainsAny(c.User, "\r\n\x00") || strings.HasPrefix(c.User, "-") {
		return nil, fmt.Errorf("invalid utm.user")
	}
	cfg.Provider = "utm"
	cfg.TargetOS = core.TargetLinux
	cfg.UTM = c
	cfg.SSHUser = c.User
	cfg.SSHKey = c.SSHKey
	cfg.WorkRoot = c.WorkRoot
	cfg.SSHFallbackPorts = nil
	return &backend{cfg: cfg, rt: rt, hostOS: runtime.GOOS}, nil
}
func (Provider) ConfigShowSection(cfg core.Config) core.ProviderConfigShowSection {
	c := cfg.UTM
	return core.ProviderConfigShowSection{JSONKey: "utm", TextLabel: "utm", Providers: []string{"utm"}, Fields: []core.ProviderConfigShowField{
		{JSONName: "appPath", JSONValue: c.AppPath, TextName: "app", TextValue: c.AppPath},
		{JSONName: "template", JSONValue: c.Template, TextName: "template", TextValue: c.Template},
		{JSONName: "user", JSONValue: c.User, TextName: "user", TextValue: c.User},
		{JSONName: "sshKey", JSONValue: c.SSHKey, TextName: "ssh_key", TextValue: c.SSHKey},
		{JSONName: "workRoot", JSONValue: c.WorkRoot, TextName: "work_root", TextValue: c.WorkRoot},
		{JSONName: "startupTimeout", JSONValue: c.StartupTimeout.String(), TextName: "startup_timeout", TextValue: c.StartupTimeout.String()}}}
}
