package modstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

type cobraRenderer struct {
	notProvisioned bool
	jsonOut        bool
	results        []Info
}

func (r *cobraRenderer) NotProvisioned(_ io.Writer, jsonOut bool) {
	r.notProvisioned = true
	r.jsonOut = jsonOut
}

func (r *cobraRenderer) Result(_ io.Writer, info Info, jsonOut bool) {
	r.results = append(r.results, info)
	r.jsonOut = jsonOut
}

func TestNewCobraCommandWiresFlagsAndNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	renderer := &cobraRenderer{}
	resolveCalls := 0

	root, optionsSource := testutil.BuildTestSubcommand(&out)
	cmd := NewCobraCommand(CobraSpec{
		Short:       "short",
		Long:        "long",
		ModuleName:  "example",
		DisplayName: "Example",
		Resolve: func(globals.Runtime) RuntimeSpec {
			resolveCalls++
			return RuntimeSpec{ProbePort: 1234, Renderer: renderer}
		},
	}, testutil.NewNilProviderRuntime(), optionsSource, &out)
	root.AddCommand(cmd)
	root.SetArgs([]string{"status", "--wait", "--json"})

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if resolveCalls != 1 {
		t.Fatalf("Resolve called %d times, want 1", resolveCalls)
	}
	if !renderer.notProvisioned || !renderer.jsonOut {
		t.Fatalf("renderer = %+v, want not-provisioned JSON", renderer)
	}
	if cmd.Use != "status" || cmd.Short != "short" || cmd.Long != "long" {
		t.Fatalf("command metadata = %q / %q / %q", cmd.Use, cmd.Short, cmd.Long)
	}
	if flag := cmd.Flags().Lookup("wait"); flag == nil || flag.Shorthand != "w" {
		t.Fatalf("wait flag = %+v, want -w", flag)
	}
}

func TestNewCobraCommandWiresProviderAndResolvedProbe(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, `{"account":"123456789012","region":"us-east-1","modules":[{"name":"example","status":"ready","resources":[{"typeName":"AWS::EC2::Instance","identifier":"i-example"}]}]}`)

	actual, err := json.Marshal(map[string]any{
		"InstanceType":     "m7i.large",
		"PrivateIpAddress": "10.0.0.7",
		"State":            map[string]any{"Name": "running"},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &testutil.TestProvider{GetResources: map[string]cloud.Resource{
		cloud.TypeAWSEC2Instance: {Identifier: "i-example", ActualState: actual},
	}}
	renderer := &cobraRenderer{}
	probedAddress := ""

	cmd := NewCobraCommand(CobraSpec{
		ModuleName:  "example",
		DisplayName: "Example",
		Resolve: func(globals.Runtime) RuntimeSpec {
			return RuntimeSpec{
				ProbePort: 4321,
				Renderer:  renderer,
				Probe: func(address string) bool {
					probedAddress = address
					return true
				},
			}
		},
	}, testutil.NewTestRuntime(provider), func() globals.Options { return globals.Options{} }, &bytes.Buffer{})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}
	if probedAddress != "10.0.0.7:4321" {
		t.Fatalf("probe address = %q", probedAddress)
	}
	if len(renderer.results) != 1 {
		t.Fatalf("results = %d, want 1", len(renderer.results))
	}
	info := renderer.results[0]
	if info.InstanceID != "i-example" || info.InstanceType != "m7i.large" || info.InstanceState != "running" {
		t.Fatalf("info = %+v", info)
	}
}

func TestNewCobraCommandReturnsRuntimeErrorBeforeResolving(t *testing.T) {
	want := errors.New("runtime unavailable")
	resolveCalled := false
	optionsCalled := false
	cmd := NewCobraCommand(CobraSpec{
		Resolve: func(globals.Runtime) RuntimeSpec {
			resolveCalled = true
			return RuntimeSpec{}
		},
	}, func() (globals.Runtime, error) {
		return globals.Runtime{}, want
	}, func() globals.Options {
		optionsCalled = true
		return globals.Options{}
	}, &bytes.Buffer{})

	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, want) {
		t.Fatalf("ExecuteContext error = %v, want %v", err, want)
	}
	if resolveCalled || optionsCalled {
		t.Fatalf("Resolve/options called after runtime failure: %v/%v", resolveCalled, optionsCalled)
	}
}
