package modstatus

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

func TestStateResolvedCobraProbeAndReadiness(t *testing.T) {
	for _, mode := range []string{"verified", "probe failure", "ready failure", "resolve failure", "TCP compatibility"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			testutil.WriteStateFile(t, dir, `{"modules":[{"name":"example","status":"provisioning","resources":[{"typeName":"AWS::EC2::Instance","identifier":"i-example"}]}]}`)
			provider := &testutil.TestProvider{GetResources: map[string]cloud.Resource{
				cloud.TypeAWSEC2Instance: {ActualState: []byte(`{"PrivateIpAddress":"10.0.1.2"}`)},
			}}
			var out bytes.Buffer
			renderer := &cobraRenderer{}
			readyCalls := 0
			cmd := NewCobraCommand(CobraSpec{
				ModuleName: "example",
				Resolve: func(globals.Runtime, context.Context) RuntimeSpec {
					return RuntimeSpec{Renderer: renderer, Probe: func(string) bool { t.Fatal("initial TCP probe used"); return false }}
				},
				ResolveState: func(_ globals.Runtime, ctx context.Context, m *state.ModuleState) (RuntimeSpec, error) {
					if ctx == nil || m.Name != "example" {
						t.Fatal("state/context lost")
					}
					if mode == "resolve failure" {
						return RuntimeSpec{}, errors.New("invalid recorded connection")
					}
					spec := RuntimeSpec{Renderer: renderer, ResourceByType: stateutil.ResourceByType,
						ProbeContext: func(ctx context.Context, ip string) error {
							if ip != "10.0.1.2" || ctx == nil {
								t.Fatal("probe target/context")
							}
							if mode == "probe failure" {
								return errors.New("certificate mismatch")
							}
							return nil
						},
						Ready: func(context.Context, *state.ModuleState) error {
							readyCalls++
							if mode == "ready failure" {
								return errors.New("state persistence failed")
							}
							return nil
						},
					}
					if mode == "TCP compatibility" {
						spec.ProbeContext = nil
						spec.Probe = func(string) bool { return true }
					}
					return spec, nil
				},
			}, testutil.NewTestRuntime(provider), func() globals.Options { return globals.Options{} }, &out)
			err := cmd.ExecuteContext(context.Background())
			if mode == "resolve failure" {
				if err == nil || readyCalls != 0 || len(renderer.results) != 0 {
					t.Fatalf("bad state continued: %v", err)
				}
				return
			}
			if err != nil || len(renderer.results) != 1 {
				t.Fatalf("status execution: %v", err)
			}
			info := renderer.results[0]
			wantReady := mode == "verified" || mode == "TCP compatibility"
			if (info.ModuleStatus == "ready") != wantReady {
				t.Fatalf("incorrect readiness: %+v", info)
			}
			if mode == "probe failure" && (readyCalls != 0 || info.ProbeError != "certificate mismatch") {
				t.Fatalf("failed TLS promoted: %+v", info)
			}
			if mode == "ready failure" && !strings.Contains(out.String(), "state persistence failed") {
				t.Fatal("write failure hidden")
			}
		})
	}
}
