package provision

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
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type contextStateProvider struct {
	testutil.TestProvider
	seen context.Context
	puts int
}

func (p *contextStateProvider) PutStateDocument(ctx context.Context, bucket, key string, data []byte) error {
	p.seen, p.puts = ctx, p.puts+1
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.TestProvider.PutStateDocument(ctx, bucket, key, data)
}

func TestStatePersistenceRequirementsAndOutcomes(t *testing.T) {
	for _, requirement := range []StateWriteRequirement{StateWriteAllowLocal, StateWriteRequireRemote} {
		for _, scenario := range []string{"remote", "explicit bucket only", "no store", "unconfigured", "absent bucket", "remote failure", "cache failure after remote", "cache failure without remote"} {
			t.Run(scenario+"/"+map[StateWriteRequirement]string{StateWriteAllowLocal: "allow local", StateWriteRequireRemote: "require remote"}[requirement], func(t *testing.T) {
				t.Chdir(t.TempDir())
				logs := captureLogs(t)
				provider := &testutil.TestProvider{}
				rt := stateTestRuntime(t, provider)
				switch scenario {
				case "explicit bucket only":
					rt.Config.Cloud.AWS.AccountID = ""
					rt.Config.State.Bucket = stateTestBucket()
				case "no store":
					rt.Provider = struct{ cloud.Provider }{Provider: provider}
				case "cache failure without remote":
					rt.Provider = nil
				case "unconfigured":
					rt.Config.Cloud.AWS.AccountID = ""
				case "absent bucket":
					provider.StateStorePutErr = cloud.ErrStateBackendAbsent
				case "remote failure":
					provider.StateStorePutErr = errors.New("storage unavailable")
				}
				blocked := strings.HasPrefix(scenario, "cache failure")
				if blocked {
					blockLocalCache(t)
				}
				st := fabricastate.NewState(stateTestAccount, "us-east-1")
				st.UpsertModule("lore", "test", "ready", nil)
				st.AddOp("lore", "create", 0)
				outcome, err := NewStatePersistence(context.Background(), rt).Persist(st, requirement)
				localOnly := scenario == "no store" || scenario == "unconfigured" || scenario == "absent bucket" || scenario == "cache failure without remote"
				wantErr := scenario == "remote failure" || scenario == "cache failure without remote" || (requirement == StateWriteRequireRemote && localOnly)
				wantRemote := !localOnly && scenario != "remote failure"
				if (err != nil) != wantErr || outcome.RemotePersisted != wantRemote {
					t.Fatalf("wrong outcome: %+v err=%v, want remote=%t error=%t", outcome, err, wantRemote, wantErr)
				}
				if errors.Is(provider.StateStorePutErr, cloud.ErrStateBackendAbsent) && requirement == StateWriteRequireRemote && !errors.Is(err, cloud.ErrStateBackendAbsent) {
					t.Fatalf("required remote write lost absent-backend cause: %v", err)
				}
				if wantRemote {
					remote, parseErr := fabricastate.ParseState([]byte(provider.StateDocuments[stateTestKey()]), stateTestAccount, "us-east-1")
					if parseErr != nil || remote.ManagedBy != "fabrica" || remote.GetModule("lore") == nil || len(remote.History) != 1 {
						t.Fatalf("successful receipt lost remote binding/inventory/history: %+v %v", remote, parseErr)
					}
				} else if len(provider.StateDocuments) != 0 {
					t.Fatal("local/error outcome changed canonical storage")
				}
				if wantErr && !blocked {
					if _, statErr := os.Stat(StatePath(rt)); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("failed write created a cache: %v", statErr)
					}
				}
				if !wantErr && !blocked {
					cache, readErr := fabricastate.ReadStateFile(StatePath(rt), stateTestAccount, "us-east-1")
					if readErr != nil || cache.ManagedBy != "fabrica" || cache.GetModule("lore") == nil || len(cache.History) != 1 {
						t.Fatalf("successful write lost cache inventory/history: %+v %v", cache, readErr)
					}
				}
				if scenario == "cache failure after remote" && !strings.Contains(logs.String(), "local cache write failed") {
					t.Fatal("remote success hid cache failure warning")
				}
				if requirement == StateWriteRequireRemote && (scenario == "no store" || scenario == "unconfigured") && st.ManagedBy != "" {
					t.Fatal("unavailable required destination bound fresh state")
				}
			})
		}
	}
}

func TestStatePersistenceRejectsInvalidWritesBeforeStorage(t *testing.T) {
	for _, scenario := range []string{"unknown requirement", "nil state", "foreign", "changed binding", "cleared binding", "custom config", "invalid config"} {
		t.Run(scenario, func(t *testing.T) {
			t.Chdir(t.TempDir())
			p := &contextStateProvider{}
			rt := stateTestRuntime(t, &p.TestProvider)
			rt.Provider = p
			cachePath := StatePath(rt)
			if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cachePath, []byte("original-cache"), 0600); err != nil {
				t.Fatal(err)
			}
			p.StateDocuments = map[string]string{stateTestKey(): "original-remote"}
			st := fabricastate.NewState("", "")
			requirement := StateWriteRequireRemote
			var err error
			switch scenario {
			case "unknown requirement":
				requirement = StateWriteRequirement(255)
			case "nil state":
				st = nil
			case "foreign", "changed binding", "cleared binding":
				st, err = fabricastate.ParseState([]byte(`{"managedBy":"Other"}`), "", "")
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "changed binding":
					st.ManagedBy = "fabrica"
				case "cleared binding":
					st.ManagedBy = ""
				}
			case "custom config":
				rt.Config.Cloud.AWS.Identity.ManagedBy = "Other"
			case "invalid config":
				rt.Config.Cloud.AWS.Identity.ManagedBy = "invalid owner"
			}
			outcome, err := NewStatePersistence(context.Background(), rt).Persist(st, requirement)
			cache, readErr := os.ReadFile(cachePath)
			if err == nil || outcome.RemotePersisted || p.puts != 0 || readErr != nil || string(cache) != "original-cache" || p.StateDocuments[stateTestKey()] != "original-remote" {
				t.Fatalf("invalid write changed storage: outcome=%+v puts=%d err=%v cacheErr=%v", outcome, p.puts, err, readErr)
			}
		})
	}
}

func TestStatePersistenceCapturesTargetAndReportsEachWrite(t *testing.T) {
	t.Chdir(t.TempDir())
	p := &testutil.TestProvider{}
	rt := stateTestRuntime(t, p)
	rt.ConfigPath, rt.Config.State.Bucket = "fabrica-studio.yaml", "initial-bucket"
	cachePath := StatePath(rt)
	writer := NewStatePersistence(context.Background(), rt)
	rt.ConfigPath, rt.Config.State.Bucket = "fabrica-other.yaml", "other-bucket"
	rt.Config.Cloud.AWS.Identity.ManagedBy = "Other"
	st := fabricastate.NewState(stateTestAccount, "us-east-1")
	st.UpsertModule("horde", "test", "ready", nil)
	first, err := writer.Persist(st, StateWriteRequireRemote)
	key := "initial-bucket/" + fabricastate.StateDocumentKey
	if err != nil || !first.RemotePersisted || len(p.StateDocuments) != 1 || !strings.Contains(p.StateDocuments[key], `"managedBy": "fabrica"`) {
		t.Fatalf("writer followed a changed destination/identity: %+v %v", first, err)
	}
	before, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	remoteBefore := p.StateDocuments[key]
	p.StateStorePutErr = cloud.ErrStateBackendAbsent
	st.UpsertModule("lore", "test", "ready", nil)
	second, err := writer.Persist(st, StateWriteRequireRemote)
	after, readErr := os.ReadFile(cachePath)
	if !errors.Is(err, cloud.ErrStateBackendAbsent) || second.RemotePersisted || p.StateDocuments[key] != remoteBefore || readErr != nil || string(after) != string(before) {
		t.Fatalf("failed second write reused success or changed storage: %+v %v %v", second, err, readErr)
	}
	if _, err := os.Stat(StatePath(rt)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("captured writer created another profile's cache: %v", err)
	}
}

func TestStatePersistenceUsesCancelledCapturedContext(t *testing.T) {
	for _, adapter := range []bool{false, true} {
		t.Run(map[bool]string{false: "required engine", true: "legacy adapter"}[adapter], func(t *testing.T) {
			t.Chdir(t.TempDir())
			p := &contextStateProvider{}
			rt := stateTestRuntime(t, &p.TestProvider)
			rt.Provider = p
			ctx, cancel := context.WithCancel(context.Background())
			writer := NewStatePersistence(ctx, rt)
			legacy := StateWriter(ctx, rt)
			cancel()
			st := fabricastate.NewState("", "")
			var outcome StateWriteOutcome
			var err error
			if adapter {
				err = legacy(st)
			} else {
				outcome, err = writer.Persist(st, StateWriteRequireRemote)
			}
			if !errors.Is(err, context.Canceled) || outcome.RemotePersisted || p.seen != ctx || p.puts != 1 || len(p.StateDocuments) != 0 {
				t.Fatalf("writer lost captured cancellation: outcome=%+v puts=%d err=%v", outcome, p.puts, err)
			}
			if _, err := os.Stat(StatePath(rt)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cancelled persistence created cache: %v", err)
			}
		})
	}
}

func TestStatePersistenceMissingConfigPreservesLegacyLocalMode(t *testing.T) {
	t.Chdir(t.TempDir())
	writer := NewStatePersistence(context.Background(), globals.Runtime{})
	st := fabricastate.NewState("", "")
	outcome, err := writer.Persist(st, StateWriteRequireRemote)
	if err == nil || outcome.RemotePersisted || st.ManagedBy != "" {
		t.Fatalf("unconfigured required write succeeded or bound state: %+v %v", outcome, err)
	}
	if _, err := os.Stat(fabricastate.DefaultStateFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unconfigured required write created cache: %v", err)
	}
	outcome, err = writer.Persist(st, StateWriteAllowLocal)
	if err != nil || outcome.RemotePersisted {
		t.Fatalf("legacy nil-config local persistence changed: %+v %v", outcome, err)
	}
	if _, err := os.Stat(fabricastate.DefaultStateFile); err != nil {
		t.Fatalf("legacy nil-config cache not written: %v", err)
	}
}
