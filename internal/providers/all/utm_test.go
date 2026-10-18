package all

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
	"github.com/openclaw/crabbox/internal/testutil"
)

func TestUTMDiscoveryConfigAndTargetAdmission(t *testing.T) {
	testutil.IsolateUserDirs(t)
	t.Chdir(t.TempDir())
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("provider: utm\nutm:\n  template: 11111111-1111-4111-8111-111111111111\n  user: prepared\n  sshKey: /tmp/prepared-key\n  startupTimeout: 3m\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRABBOX_CONFIG", config)
	t.Setenv("CRABBOX_UTM_USER", "from-environment")
	p, err := core.ProviderFor("utm")
	if err != nil {
		t.Fatal(err)
	}
	if p.Spec().Kind != core.ProviderKindSSHLease || !p.Spec().Features.Has(core.FeatureCrabboxSync) || p.Spec().Features.Has(core.FeatureDesktop) || p.Spec().Features.Has(core.FeatureCheckpoint) {
		t.Fatalf("inaccurate UTM capabilities: %#v", p.Spec())
	}
	cfg, err := core.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UTM.User != "from-environment" || cfg.UTM.StartupTimeout.String() != "3m0s" || cfg.Architecture != "arm64" {
		t.Fatalf("typed UTM configuration not loaded: %#v arch=%s", cfg.UTM, cfg.Architecture)
	}
	configured, err := p.Configure(cfg, core.Runtime{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := configured.(core.SSHLeaseBackend); !ok {
		t.Fatal("UTM must route run/sync through the core SSH lease backend")
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	values := p.RegisterFlags(fs, cfg)
	if err = fs.Parse([]string{"--utm-user=override", "--utm-template=22222222-2222-4222-8222-222222222222"}); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyFlags(&cfg, fs, values); err != nil {
		t.Fatal(err)
	}
	if cfg.UTM.User != "override" || cfg.UTM.SSHKey != "/tmp/prepared-key" {
		t.Fatal("flag application clobbered absent fields")
	}
	for _, args := range [][]string{{"providers", "describe", "utm", "--json"}, {"config", "show", "--json"}} {
		var out bytes.Buffer
		if err = (core.App{Stdout: &out, Stderr: io.Discard}).Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "utm") {
			t.Fatalf("UTM absent from discovery/config: %s", out.String())
		}
	}
	for _, target := range []string{core.TargetMacOS, core.TargetWindows} {
		cfg.TargetOS = target
		if _, err = p.Configure(cfg, core.Runtime{}); err == nil {
			t.Fatalf("unsupported UTM target %s accepted", target)
		}
	}
}
