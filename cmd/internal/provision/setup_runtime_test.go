package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

type setupIdentityProvider struct {
	account, region string
	err             error
	calls           int
}

func (*setupIdentityProvider) Name() string                    { return "test" }
func (*setupIdentityProvider) Resources() cloud.ResourceClient { return nil }
func (p *setupIdentityProvider) Identity(ctx context.Context) (string, string, string, error) {
	p.calls++
	if err := ctx.Err(); err != nil {
		return "", "", "", err
	}
	return p.account, "", p.region, p.err
}

func TestPrepareSetupRuntimeResolvesCopyWithoutWrites(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "inferred", true: "explicit"}[explicit], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fabrica-studio.yaml")
			original := []byte("# preserved configuration\ncloud:\n  provider: aws\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{}
			cfg.Cloud.AWS.Tags = map[string]string{"Team": "Tools"}
			if explicit {
				cfg.Cloud.AWS.AccountID = "account-one"
				cfg.Cloud.AWS.Region = "us-west-2"
				cfg.State.Bucket, cfg.State.Table = "chosen-bucket", "chosen-table"
			}
			before := cfg.Clone()
			p := &setupIdentityProvider{account: "account-one", region: "us-west-2"}
			rt := globals.Runtime{Config: cfg, Provider: p, ConfigPath: path}
			prepared, plan, err := PrepareSetupRuntime(context.Background(), rt)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Config == cfg || !reflect.DeepEqual(cfg, before) || p.calls != 1 {
				t.Fatal("preparation changed the original config or repeated identity I/O")
			}
			if prepared.Provider != p || prepared.ConfigPath != path {
				t.Fatal("preparation changed provider/profile cache selection")
			}
			names := state.ResolveBackendNames(cfg, "account-one")
			if plan.Account != "account-one" || plan.Region != "us-west-2" || plan.Backend != names ||
				prepared.Config.Cloud.AWS.AccountID != plan.Account || prepared.Config.Cloud.AWS.Region != plan.Region ||
				prepared.Config.State.Bucket != names.Bucket || prepared.Config.State.Table != names.Table {
				t.Fatalf("effective runtime does not contain the resolved read/lock target: %+v %+v", prepared.Config, plan)
			}
			prepared.Config.Cloud.AWS.Tags["Team"] = "Changed"
			if cfg.Cloud.AWS.Tags["Team"] != "Tools" {
				t.Fatal("effective runtime aliases original operator tags")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(original) {
				t.Fatalf("read-only preparation saved config: %v", err)
			}
		})
	}
}

func TestPrepareSetupRuntimeRejectsUnsafeTarget(t *testing.T) {
	for _, tt := range []struct {
		name, account, region, owner, providerAccount, providerRegion, want string
		nilConfig, nilProvider                                              bool
		err                                                                 error
	}{
		{name: "missing config", nilConfig: true, want: "configuration"},
		{name: "missing provider", nilProvider: true, want: "provider"},
		{name: "custom guard before provider", owner: "Studio-Tools", nilProvider: true, want: "custom ownership is not enabled"},
		{name: "invalid identity", owner: "old owner", want: "managedBy"},
		{name: "identity failure", err: errors.New("credentials unavailable"), want: "credentials unavailable"},
		{name: "empty account", providerRegion: "us-west-2", want: "account"},
		{name: "empty region", providerAccount: "account-one", want: "region"},
		{name: "wrong account", account: "account-two", providerAccount: "account-one", providerRegion: "us-west-2", want: "target mismatch"},
		{name: "wrong region", region: "us-east-1", providerAccount: "account-one", providerRegion: "us-west-2", want: "target mismatch"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Cloud.AWS.Tags = map[string]string{}
			cfg.Cloud.AWS.AccountID, cfg.Cloud.AWS.Region, cfg.Cloud.AWS.Identity.ManagedBy = tt.account, tt.region, tt.owner
			p := &setupIdentityProvider{account: tt.providerAccount, region: tt.providerRegion, err: tt.err}
			rt := globals.Runtime{Config: cfg, Provider: p}
			if tt.nilConfig {
				rt.Config = nil
			}
			if tt.nilProvider {
				rt.Provider = nil
			}
			before := cfg.Clone()
			prepared, plan, err := PrepareSetupRuntime(context.Background(), rt)
			if err == nil || !strings.Contains(err.Error(), tt.want) || !reflect.DeepEqual(prepared, globals.Runtime{}) || !reflect.DeepEqual(plan, state.SetupPlan{}) {
				t.Fatalf("unsafe target prepared: runtime=%+v plan=%+v err=%v", prepared, plan, err)
			}
			if !reflect.DeepEqual(cfg, before) {
				t.Fatal("refusal changed original config")
			}
			if tt.owner != "" && p.calls != 0 {
				t.Fatal("identity guard performed provider I/O")
			}
			if strings.Contains(err.Error(), "delete") || strings.Contains(err.Error(), "retag") {
				t.Fatalf("refusal suggested bypass: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := PrepareSetupRuntime(ctx, globals.Runtime{Config: &config.Config{}, Provider: &setupIdentityProvider{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("identity resolution lost cancellation: %v", err)
	}
}
