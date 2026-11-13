package provision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/oplog"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

const stateTestAccount = "123456789012"

func TestReadState_RejectsOwnershipBeforeCacheRefresh(t *testing.T) {
	const foreign = `{"managedBy":"Studio-Tools","modules":[{"name":"foreign"}]}`
	for _, tc := range []struct {
		name, source, doc, wantErr string
	}{
		{"remote foreign", "remote", foreign, "state ownership mismatch"},
		{"missing remote fallback", "fallback", foreign, "state ownership mismatch"},
		{"local only", "local", foreign, "state ownership mismatch"},
		{"remote empty foreign", "remote", `{"managedBy":"Studio-Tools","modules":[]}`, "state ownership mismatch"},
		{"fallback empty foreign", "fallback", `{"managedBy":"Studio-Tools","modules":[]}`, "state ownership mismatch"},
		{"remote invalid binding", "remote", `{"managedBy":null}`, "state managedBy"},
		{"remote malformed JSON", "remote", `{`, "parsing state document"},
		{"remote target mismatch", "remote", `{"account":"other-account"}`, "state target mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			provider := &testutil.TestProvider{}
			rt := stateTestRuntime(t, provider)
			if err := fabricastate.WriteState(fabricastate.NewState(stateTestAccount, "us-east-1")); err != nil {
				t.Fatal(err)
			}
			if tc.source == "remote" {
				provider.StateDocuments = map[string]string{stateTestKey(): tc.doc}
			} else {
				if err := os.WriteFile(StatePath(rt), []byte(tc.doc), 0600); err != nil {
					t.Fatal(err)
				}
				if tc.source == "local" {
					rt.Provider = nil
				}
			}
			before, err := os.ReadFile(StatePath(rt))
			if err != nil {
				t.Fatal(err)
			}
			st, err := ReadState(context.Background(), rt)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || st != nil {
				t.Fatalf("foreign state returned: state=%+v, error=%v", st, err)
			}
			after, err := os.ReadFile(StatePath(rt))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected read changed cache: %v", err)
			}
			if tc.source == "remote" && provider.StateDocuments[stateTestKey()] != tc.doc {
				t.Fatal("read changed canonical document")
			}
		})
	}
}

func TestStateWriter_RejectsLoadedOwnershipBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   bool
		changeTo string
		wantErr  string
	}{
		{name: "foreign", wantErr: "state ownership mismatch"},
		{name: "changed", change: true, changeTo: "fabrica", wantErr: "binding changed"},
		{name: "cleared", change: true, wantErr: "binding changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			provider := &testutil.TestProvider{StateDocuments: map[string]string{stateTestKey(): "original-remote"}}
			rt := stateTestRuntime(t, provider)
			if err := fabricastate.WriteState(fabricastate.NewState(stateTestAccount, "us-east-1")); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(StatePath(rt))
			if err != nil {
				t.Fatal(err)
			}
			st, err := fabricastate.ParseState([]byte(`{"managedBy":"Studio-Tools"}`), "", "")
			if err != nil {
				t.Fatal(err)
			}
			if tc.change {
				st.ManagedBy = tc.changeTo
			}
			err = StateWriter(context.Background(), rt)(st)
			for _, want := range []string{tc.wantErr, "Studio-Tools", "restore"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("write error = %v, want %q", err, want)
				}
			}
			if !tc.change && !strings.Contains(err.Error(), "fabrica") {
				t.Fatal("mismatch omitted configured owner")
			}
			if strings.Contains(err.Error(), "delete") || strings.Contains(err.Error(), "retag") {
				t.Fatal("mismatch guidance must not suggest bypassing the binding")
			}
			after, err := os.ReadFile(StatePath(rt))
			if err != nil || !bytes.Equal(before, after) || provider.StateDocuments[stateTestKey()] != "original-remote" {
				t.Fatalf("rejected write changed storage: %v", err)
			}
		})
	}
}

func TestStateIO_LegacyReadsAndFreshAbsence(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{}
	rt := stateTestRuntime(t, provider)
	st, err := ReadState(context.Background(), rt)
	if err != nil || st.ManagedBy != "" {
		t.Fatalf("absent state must remain unbound: %+v, %v", st, err)
	}
	if _, err := os.Stat(StatePath(rt)); !os.IsNotExist(err) || len(provider.StateDocuments) != 0 {
		t.Fatal("reading absent state created storage")
	}
	legacy := `{"modules":[],"history":[]}`
	provider.StateDocuments = map[string]string{stateTestKey(): legacy}
	st, err = ReadState(context.Background(), rt)
	if err != nil || st.ManagedBy != "fabrica" {
		t.Fatalf("legacy state was not default-bound: %+v, %v", st, err)
	}
	if len(provider.StateDocuments) != 1 || provider.StateDocuments[stateTestKey()] != legacy {
		t.Fatal("legacy read changed canonical remote bytes")
	}
	cache, err := os.ReadFile(StatePath(rt))
	if err != nil || !bytes.Contains(cache, []byte(`"managedBy": "fabrica"`)) {
		t.Fatalf("legacy cache refresh lost the resolved default binding: %v", err)
	}
}

func TestStateWriter_PersistsDefaultOwnership(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("remote=%t/legacy=%t", remote, legacy), func(t *testing.T) {
				t.Chdir(t.TempDir())
				provider := &testutil.TestProvider{}
				rt := stateTestRuntime(t, provider)
				if !remote {
					rt.Provider = nil
				}
				st := fabricastate.NewState(stateTestAccount, "us-east-1")
				if legacy {
					var err error
					st, err = fabricastate.ParseState(fabricastate.EncodeState(st), stateTestAccount, "us-east-1")
					if err != nil {
						t.Fatal(err)
					}
				}
				st.UpsertModule("lore", "test", "ready", nil)
				st.AddOp("lore", "create", 0)
				if err := StateWriter(context.Background(), rt)(st); err != nil {
					t.Fatal(err)
				}
				cache, err := fabricastate.ReadStateFile(StatePath(rt), stateTestAccount, "us-east-1")
				if err != nil || cache.ManagedBy != "fabrica" || cache.GetModule("lore") == nil || len(cache.History) != 1 {
					t.Fatalf("write lost binding or inventory: %+v, %v", cache, err)
				}
				if remote && !strings.Contains(provider.StateDocuments[stateTestKey()], `"managedBy": "fabrica"`) {
					t.Fatal("canonical remote write omitted default binding")
				}
			})
		}
	}
}

func TestStateIO_ProgrammaticCustomConfigAndNilWriteRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{StateStoreGetErr: errors.New("remote read must not run")}
	rt := stateTestRuntime(t, provider)
	rt.Config.Cloud.AWS.Identity.ManagedBy = "Studio-Tools"
	if _, err := ReadState(context.Background(), rt); err == nil || !strings.Contains(err.Error(), "custom ownership is not enabled") {
		t.Fatalf("programmatic custom read not blocked before remote I/O: %v", err)
	}
	st := fabricastate.NewState("", "")
	if err := StateWriter(context.Background(), rt)(st); err == nil || !strings.Contains(err.Error(), "custom ownership is not enabled") || st.ManagedBy != "" {
		t.Fatalf("programmatic custom write not blocked before binding: %v", err)
	}
	rt.Config.Cloud.AWS.Identity.ManagedBy = ""
	if err := StateWriter(context.Background(), rt)(nil); err == nil {
		t.Fatal("nil state accepted for writing")
	}
	if _, err := os.Stat(StatePath(rt)); !os.IsNotExist(err) || len(provider.StateDocuments) != 0 {
		t.Fatal("rejected state operation changed storage")
	}
}

// stateTestRuntime builds a runtime with a Store-capable provider and a
// bootstrapped account, so provision state I/O exercises the remote path.
func stateTestRuntime(t *testing.T, provider *testutil.TestProvider) globals.Runtime {
	t.Helper()
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = stateTestAccount
	cfg.Cloud.AWS.Region = "us-east-1"
	return globals.Runtime{Config: cfg, Provider: provider}
}

func stateTestBucket() string {
	return "fabrica-state-" + stateTestAccount
}

func stateTestKey() string {
	return stateTestBucket() + "/" + fabricastate.StateDocumentKey
}

// blockLocalCache makes the default state file a directory so every local
// cache write fails.
func blockLocalCache(t *testing.T) {
	t.Helper()
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(filepath.Dir(fabricastate.DefaultStateFile), 0700); err != nil {
		t.Fatalf("creating state directory: %v", err)
	}
	// #nosec G301 -- directory needs execute for traversal
	if err := os.Mkdir(fabricastate.DefaultStateFile, 0700); err != nil {
		t.Fatalf("blocking local cache: %v", err)
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)
	return &buf
}

// Two independent local directories sharing one remote backend: dir A
// provisions and persists; dir B (fresh checkout, no local cache) must read
// the same inventory from the remote document.
func TestReadState_RemoteIsCanonicalAcrossDirectories(t *testing.T) {
	provider := &testutil.TestProvider{}
	ctx := context.Background()

	t.Run("dir A persists to remote", func(t *testing.T) {
		t.Chdir(t.TempDir())
		rt := stateTestRuntime(t, provider)

		st, err := ReadState(ctx, rt)
		if err != nil {
			t.Fatalf("ReadState: %v", err)
		}
		st.Modules = append(st.Modules, fabricastate.ModuleState{Name: "perforce", Status: "ready"})
		if err := StateWriter(ctx, rt)(st); err != nil {
			t.Fatalf("StateWriter: %v", err)
		}

		doc, ok := provider.StateDocuments[stateTestKey()]
		if !ok {
			t.Fatalf("remote document not written; provider docs = %v", provider.StateDocuments)
		}
		remote, err := fabricastate.ParseState([]byte(doc), stateTestAccount, "us-east-1")
		if err != nil {
			t.Fatalf("ParseState(remote doc): %v", err)
		}
		if len(remote.Modules) != 1 || remote.Modules[0].Name != "perforce" {
			t.Fatalf("remote modules = %+v, want perforce", remote.Modules)
		}
	})

	t.Run("dir B reads inventory without local cache", func(t *testing.T) {
		t.Chdir(t.TempDir())
		rt := stateTestRuntime(t, provider)

		if _, err := os.Stat(fabricastate.DefaultStateFile); !os.IsNotExist(err) {
			t.Fatalf("precondition: dir B must have no local state file, got %v", err)
		}
		st, err := ReadState(ctx, rt)
		if err != nil {
			t.Fatalf("ReadState: %v", err)
		}
		if len(st.Modules) != 1 || st.Modules[0].Name != "perforce" || st.Modules[0].Status != "ready" {
			t.Fatalf("state read from remote = %+v, want the perforce module dir A recorded", st.Modules)
		}
		if _, err := os.Stat(fabricastate.DefaultStateFile); err != nil {
			t.Fatalf("local cache not refreshed in dir B: %v", err)
		}
	})
}

func TestReadState_RemoteErrorSurfaces(t *testing.T) {
	t.Chdir(t.TempDir())
	cause := errors.New("s3 unavailable")
	provider := &testutil.TestProvider{StateStoreGetErr: cause}
	rt := stateTestRuntime(t, provider)

	_, err := ReadState(context.Background(), rt)
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want wrapped %v", err, cause)
	}
	if !strings.Contains(err.Error(), "reading remote state") {
		t.Fatalf("err = %v, want remote read context", err)
	}
}

func TestReadState_MissingRemoteDocFallsBackToLocal(t *testing.T) {
	empty := fabricastate.NewState(stateTestAccount, "us-east-1")
	populated := fabricastate.NewState(stateTestAccount, "us-east-1")
	populated.Modules = append(populated.Modules, fabricastate.ModuleState{Name: "lore"})
	for _, tt := range []struct {
		name       string
		cache      []byte
		blockCache bool
		wantErr    string
		wantModule bool
	}{
		{name: "missing cache"},
		{name: "empty cache", cache: fabricastate.EncodeState(empty)},
		{name: "populated cache", cache: fabricastate.EncodeState(populated), wantModule: true},
		{name: "malformed cache", cache: []byte("{"), wantErr: "parsing state document"},
		{name: "account mismatch", cache: fabricastate.EncodeState(fabricastate.NewState("other-account", "us-east-1")), wantErr: "state target mismatch"},
		{name: "region mismatch", cache: fabricastate.EncodeState(fabricastate.NewState(stateTestAccount, "us-west-2")), wantErr: "state target mismatch"},
		{name: "unreadable cache", blockCache: true, wantErr: "reading state file"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buf := captureLogs(t)
			t.Chdir(t.TempDir())
			if tt.blockCache {
				blockLocalCache(t)
			} else if tt.cache != nil {
				if err := os.MkdirAll(filepath.Dir(fabricastate.DefaultStateFile), 0700); err != nil {
					t.Fatalf("creating cache directory: %v", err)
				}
				if err := os.WriteFile(fabricastate.DefaultStateFile, tt.cache, 0600); err != nil {
					t.Fatalf("seeding local cache: %v", err)
				}
			}
			provider := &testutil.TestProvider{} // nothing stored remotely
			st, err := ReadState(context.Background(), stateTestRuntime(t, provider))
			logs := buf.String()
			if warned := strings.Contains(logs, "level=WARN"); warned != tt.wantModule {
				t.Fatalf("warning = %t, want %t; logs:\n%s", warned, tt.wantModule, logs)
			}
			if len(provider.StateDocuments) != 0 {
				t.Fatal("reading a missing document must not write remote state")
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ReadState error = %v, want %q", err, tt.wantErr)
				}
				if st != nil {
					t.Fatal("invalid cache returned usable state")
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			wantCount, wantLevel := 0, "level=DEBUG"
			wantMessage := "remote state empty; no modules provisioned yet"
			if tt.wantModule {
				wantCount, wantLevel = 1, "level=WARN"
				wantMessage = "remote state document not found; using local state cache"
			}
			if len(st.Modules) != wantCount || st.Account != stateTestAccount || st.Region != "us-east-1" {
				t.Fatalf("state = %+v, want %d modules with runtime identity", st, wantCount)
			}
			if tt.wantModule && st.Modules[0].Name != "lore" {
				t.Fatalf("state = %+v, want the seeded local cache", st.Modules)
			}
			if !strings.Contains(logs, wantMessage) || !strings.Contains(logs, wantLevel) {
				t.Fatalf("expected %s diagnostic %q, got:\n%s", wantLevel, wantMessage, logs)
			}
		})
	}
}

func TestStateWriter_RemoteErrorFailsMutationAndKeepsLocal(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{StateStorePutErr: errors.New("s3 put failed")}
	rt := stateTestRuntime(t, provider)

	before := fabricastate.NewState(stateTestAccount, "us-east-1")
	if err := fabricastate.WriteState(before); err != nil {
		t.Fatalf("seeding local cache: %v", err)
	}
	original, err := os.ReadFile(fabricastate.DefaultStateFile)
	if err != nil {
		t.Fatalf("reading seeded cache: %v", err)
	}

	after := fabricastate.NewState(stateTestAccount, "us-east-1")
	after.Modules = append(after.Modules, fabricastate.ModuleState{Name: "horde"})

	err = StateWriter(context.Background(), rt)(after)
	if err == nil || !strings.Contains(err.Error(), "persisting remote state") {
		t.Fatalf("err = %v, want remote persist failure", err)
	}
	still, err := os.ReadFile(fabricastate.DefaultStateFile)
	if err != nil {
		t.Fatalf("reading local cache after failed write: %v", err)
	}
	if !bytes.Equal(still, original) {
		t.Fatal("local cache modified after a failed remote persist")
	}
}

func TestStateWriter_BucketAbsentDegradesToLocalOnly(t *testing.T) {
	buf := captureLogs(t)
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{StateStorePutErr: fabricac.ErrStateBackendAbsent}
	rt := stateTestRuntime(t, provider)

	st := fabricastate.NewState(stateTestAccount, "us-east-1")
	if err := StateWriter(context.Background(), rt)(st); err != nil {
		t.Fatalf("bucket-absent write must degrade to local-only, got %v", err)
	}
	if _, err := os.Stat(fabricastate.DefaultStateFile); err != nil {
		t.Fatalf("local cache not written: %v", err)
	}
	if !strings.Contains(buf.String(), "remote state bucket not found") {
		t.Fatalf("expected bucket-absent warning, got:\n%s", buf)
	}
}

func TestStateWriter_RemoteOKLocalFailSucceedsWithWarning(t *testing.T) {
	buf := captureLogs(t)
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{}
	rt := stateTestRuntime(t, provider)
	blockLocalCache(t)

	st := fabricastate.NewState(stateTestAccount, "us-east-1")
	if err := StateWriter(context.Background(), rt)(st); err != nil {
		t.Fatalf("local cache failure after a successful remote write must not fail the mutation: %v", err)
	}
	if _, ok := provider.StateDocuments[stateTestKey()]; !ok {
		t.Fatalf("remote document not written; provider docs = %v", provider.StateDocuments)
	}
	if !strings.Contains(buf.String(), "remote state persisted; local cache write failed") {
		t.Fatalf("expected local-cache-failure warning, got:\n%s", buf)
	}
}

func TestStateWriter_LocalFailureWithoutRemoteFails(t *testing.T) {
	t.Chdir(t.TempDir())
	rt := globals.Runtime{Config: config.Defaults()} // no provider, no remote
	blockLocalCache(t)

	st := fabricastate.NewState(stateTestAccount, "us-east-1")
	if err := StateWriter(context.Background(), rt)(st); err == nil {
		t.Fatal("expected the local cache write error to surface")
	}
}

// An unbootstrapped account (no account ID, no explicit bucket) must skip the
// remote backend entirely, even when the provider supports it.
func TestStateIO_UnbootstrappedAccountSkipsRemote(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{StateStoreGetErr: errors.New("remote must not be consulted")}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}

	st, err := ReadState(context.Background(), rt)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.Account != "" {
		t.Fatalf("expected empty account, got %q", st.Account)
	}
	st.Modules = append(st.Modules, fabricastate.ModuleState{Name: "ddc"})
	if err := StateWriter(context.Background(), rt)(st); err != nil {
		t.Fatalf("StateWriter: %v", err)
	}
	if len(provider.StateDocuments) != 0 {
		t.Fatalf("remote consulted for an unbootstrapped account: %v", provider.StateDocuments)
	}
}

// An explicit state.bucket override is used even when the account ID is not
// recorded yet.
func TestStateWriter_ExplicitBucketUsedWithoutAccountID(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{}
	cfg := config.Defaults()
	cfg.State.Bucket = "custom-state-bucket"
	rt := globals.Runtime{Config: cfg, Provider: provider}

	st := fabricastate.NewState("", "us-east-1")
	if err := StateWriter(context.Background(), rt)(st); err != nil {
		t.Fatalf("StateWriter: %v", err)
	}
	key := "custom-state-bucket/" + fabricastate.StateDocumentKey
	if _, ok := provider.StateDocuments[key]; !ok {
		t.Fatalf("explicit bucket not used; provider docs = %v", provider.StateDocuments)
	}
}
