package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func guardedCommand(t *testing.T) (command, *testutil.BackendProvider) {
	t.Helper()
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID, cfg.Cloud.AWS.Region = "", "us-west-2"
	cfg.State.Bucket, cfg.State.Table = "b", "t"
	p := &testutil.BackendProvider{TestProvider: &testutil.TestProvider{AccountID: "account-one", Region: "us-west-2"}}
	return command{runtime: globals.Runtime{Config: cfg, Provider: p, ConfigPath: "fabrica.yaml"},
		out: &strings.Builder{}, costs: cost.Global, version: "test", assumeYes: true,
		applyBackend: state.ApplyBackendSetup}, p
}

func writeSetupCache(t *testing.T, data string) {
	t.Helper()
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.DefaultStateFile, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGuardedSetupRefusesEveryUnsafePhase(t *testing.T) {
	const canonical = `{"account":"account-one","region":"us-west-2","modules":[{"name":"horde"}],"history":[{"module":"horde","action":"create"}]}`
	for _, tc := range []struct {
		name, want string
		attempts   int
	}{
		{"custom", "custom ownership", 0}, {"target", "target mismatch", 0}, {"identity", "injected", 0},
		{"version", "version tag", 0},
		{"metadata-capability", "cannot inspect", 0}, {"store-capability", "cannot read", 0},
		{"bucket-read", "injected", 0}, {"table-read", "injected", 0}, {"document-read", "injected", 0},
		{"canonical-null", "canonical backend state", 0}, {"canonical-malformed", "canonical backend state", 0},
		{"canonical-owner", "ownership", 0}, {"canonical-target", "target mismatch", 0},
		{"cache-owner", "ownership mismatch", 0}, {"cache-target", "target mismatch", 0},
		{"cache-region", "target mismatch", 0}, {"cache-malformed", "reading state file", 0},
		{"cache-during-confirm", "ownership mismatch", 0},
		{"appeared-owned", "changed since preview", 0}, {"appeared-foreign", "ownership", 0},
		{"appeared-untagged", "ownership", 0}, {"appeared-table", "changed since preview", 0},
		{"disappeared", "changed since preview", 0}, {"changed-owner", "ownership", 0},
		{"no-creator", "cannot create", 0}, {"no-reconciler", "cannot reconcile", 0},
		{"bucket-write", "injected", 1}, {"table-write", "injected", 2},
		{"final-bucket-read", "injected", 2}, {"final-table-read", "injected", 2},
		{"final-document-read", "injected", 2}, {"final-foreign", "ownership", 2},
		{"final-absent", "completion", 2}, {"final-repair", "completion", 2},
		{"final-cache", "ownership mismatch", 2}, {"final-null", "canonical backend state", 2},
		{"existing-lock-missing", "not acquired", 0}, {"existing-lock-failure", "injected", 0},
		{"post-lock-missing", "no live state lock", 2}, {"post-lock-failure", "injected", 2},
		{"existing-lock-cancel", "context canceled", 2},
		{"cancel-before-apply", "context canceled", 0}, {"cancel-after-apply", "context canceled", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p := guardedCommand(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cache := ""
			p.BackendErr = make(map[string]error)
			if strings.HasPrefix(tc.name, "canonical-") {
				p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				doc := canonical
				switch tc.name {
				case "canonical-null":
					doc = "null"
				case "canonical-malformed":
					doc = "{"
				case "canonical-owner":
					doc = `{"managedBy":"tools",` + canonical[1:]
				case "canonical-target":
					doc = strings.Replace(canonical, "account-one", "account-two", 1)
				}
				p.StateDocuments = map[string]string{"b/" + state.StateDocumentKey: doc}
			}
			switch tc.name {
			case "identity":
				p.IdentityErr = errors.New("injected")
			case "version":
				c.version = ""
			case "custom":
				c.runtime.Config.Cloud.AWS.Identity.ManagedBy = "tools"
				p.IdentityErr = errors.New("provider must not be called")
			case "target":
				c.runtime.Config.Cloud.AWS.AccountID = "account-two"
			case "metadata-capability":
				c.runtime.Provider = p.TestProvider
			case "store-capability":
				c.runtime.Provider = struct {
					cloud.Provider
					cloud.StateBackendMetadataReader
				}{p, p}
			case "no-creator", "no-reconciler":
				c.runtime.Provider = struct {
					cloud.Provider
					cloud.StateBackendMetadataReader
					cloud.StateStore
				}{p, p, p}
				if tc.name == "no-reconciler" {
					p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				}
			case "bucket-read", "table-read", "document-read", "bucket-write", "table-write":
				operation := map[string]string{"bucket-read": "bucket metadata", "table-read": "table metadata", "document-read": "get document", "bucket-write": "create bucket", "table-write": "create table"}[tc.name]
				p.BackendErr[operation] = errors.New("injected")
			case "cache-owner", "cache-during-confirm":
				cache = `{"managedBy":"tools"}`
			case "cache-target":
				cache = `{"account":"account-two"}`
			case "cache-region":
				cache = `{"region":"us-east-1"}`
			case "cache-malformed":
				cache = "{"
			case "disappeared", "changed-owner":
				p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
			}
			if strings.Contains(tc.name, "-lock-") {
				c.runtime.Provider = &testutil.BackendLockingProvider{BackendProvider: p}
				if strings.HasPrefix(tc.name, "existing-") {
					p.Table = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				}
				if strings.HasSuffix(tc.name, "missing") {
					p.BackendErr["acquire row"] = cloud.ErrLockTableMissing
				} else if strings.HasSuffix(tc.name, "failure") {
					p.BackendErr["acquire row"] = errors.New("injected")
				}
			}
			if cache != "" && tc.name != "cache-during-confirm" {
				writeSetupCache(t, cache)
			}
			c.assumeYes = false
			c.confirm = func(string) bool {
				switch tc.name {
				case "cache-during-confirm":
					writeSetupCache(t, cache)
				case "appeared-owned", "appeared-foreign", "appeared-untagged":
					p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
					switch tc.name {
					case "appeared-foreign":
						p.Bucket.Tags[tags.ManagedByKey] = "tools"
					case "appeared-untagged":
						p.Bucket.Tags = nil
					}
				case "appeared-table":
					p.Table = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				case "disappeared":
					p.Bucket.Exists, p.Bucket.Tags = false, nil
				case "changed-owner":
					p.Bucket.Tags[tags.ManagedByKey] = "tools"
				case "cancel-before-apply":
					cancel()
				}
				return true
			}
			c.applyBackend = func(ctx context.Context, provider cloud.Provider, plan state.BackendSetupApplyPlan) ([]state.BootstrapResult, error) {
				results, err := state.ApplyBackendSetup(ctx, provider, plan)
				switch tc.name {
				case "final-bucket-read", "final-table-read", "final-document-read":
					operation := map[string]string{"final-bucket-read": "bucket metadata", "final-table-read": "table metadata", "final-document-read": "get document"}[tc.name]
					p.BackendErr[operation] = errors.New("injected")
				case "final-foreign", "final-repair":
					p.Bucket.Tags[tags.ManagedByKey] = "old tools"
					if tc.name == "final-repair" {
						p.StateDocuments = map[string]string{"b/" + state.StateDocumentKey: canonical}
					}
				case "final-absent":
					p.Table.Exists = false
				case "final-cache":
					cache = `{"managedBy":"tools"}`
					writeSetupCache(t, cache)
				case "final-null":
					p.StateDocuments = map[string]string{"b/" + state.StateDocumentKey: "null"}
				case "cancel-after-apply", "existing-lock-cancel":
					cancel()
				}
				return results, err
			}
			beforeAccount := c.runtime.Config.Cloud.AWS.AccountID
			err := c.run(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe phase %s: %v", tc.name, err)
			}
			attempts := 0
			for _, call := range p.BackendCalls {
				if strings.HasPrefix(call.Operation, "create ") || strings.HasPrefix(call.Operation, "reconcile ") {
					attempts++
				}
				if call.Operation == "put document" || call.Operation == "acquire row" && !strings.Contains(tc.name, "-lock-") {
					t.Fatalf("unexpected write: %+v", call)
				}
			}
			if attempts != tc.attempts {
				t.Fatalf("component attempts = %d, want %d", attempts, tc.attempts)
			}
			if c.runtime.Config.Cloud.AWS.AccountID != beforeAccount {
				t.Fatal("failed setup changed the original runtime")
			}
			if locker, ok := c.runtime.Provider.(*testutil.BackendLockingProvider); ok && len(locker.LockRow("t", "fabrica-state/account-one")) != 0 {
				t.Fatal("failed setup leaked an acquired row")
			}
			if _, err := os.Stat("fabrica.yaml"); !os.IsNotExist(err) {
				t.Fatalf("failed setup saved configuration: %v", err)
			}
			if cache != "" {
				got, err := os.ReadFile(state.DefaultStateFile)
				if err != nil || string(got) != cache {
					t.Fatalf("cache changed: %s %v", got, err)
				}
			} else if _, err := os.Stat(filepath.Dir(state.DefaultStateFile)); !os.IsNotExist(err) {
				t.Fatalf("setup created a cache directory: %v", err)
			}
		})
	}
}

func TestGuardedSetupPreservesStateAndLocalIsNeverRepairProof(t *testing.T) {
	const legacy = `{"account":"account-one","region":"us-west-2","modules":[{"name":"horde"}],"history":[{"module":"horde","action":"create"}]}`
	for _, mode := range []string{"absent", "local-legacy", "local-default", "canonical", "historical-repair", "local-cannot-repair", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			c, p := guardedCommand(t)
			cache, remote := "", ""
			if mode != "absent" && mode != "rejected" {
				cache = legacy
				if mode == "local-default" {
					cache = `{"managedBy":"fabrica",` + legacy[1:]
				}
				if mode == "canonical" || mode == "historical-repair" {
					remote, cache = legacy, `{"managedBy":"foreign-cache"}`
					p.StateDocuments = map[string]string{"b/" + state.StateDocumentKey: remote}
				}
				writeSetupCache(t, cache)
				p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				if strings.Contains(mode, "repair") {
					p.Bucket.Tags[tags.ManagedByKey] = "旧 historical owner"
				}
			}
			if mode == "rejected" {
				c.assumeYes, c.confirm = false, func(string) bool { return false }
				p.Table = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				c.runtime.Provider = &testutil.BackendLockingProvider{BackendProvider: p}
			}
			err := c.run(context.Background())
			if mode == "local-cannot-repair" {
				if err == nil {
					t.Fatal("local cache authorized historical repair")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			for _, call := range p.BackendCalls {
				if call.Operation == "put document" || call.Operation == "acquire row" || (mode == "rejected" || mode == "local-cannot-repair") && (strings.HasPrefix(call.Operation, "create ") || strings.HasPrefix(call.Operation, "reconcile ")) {
					t.Fatalf("unexpected write: %+v", call)
				}
			}
			if p.StateDocuments["b/"+state.StateDocumentKey] != remote {
				t.Fatal("setup changed canonical bytes")
			}
			if cache != "" {
				if got, err := os.ReadFile(state.DefaultStateFile); err != nil || string(got) != cache {
					t.Fatalf("setup changed local bytes: %s %v", got, err)
				}
			} else if _, err := os.Stat(".fabrica"); !os.IsNotExist(err) {
				t.Fatalf("setup created a cache: %v", err)
			}
		})
	}
}
