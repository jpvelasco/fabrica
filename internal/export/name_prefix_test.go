package export

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestGenerateOutputRefusesForeignRecordedPrefix(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	if err := st.BindNamePrefix(naming.MustResolve("acme")); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateOutput(naming.Default, CloudFormation, st, config.Defaults()); err == nil {
		t.Fatal("default prefix exported acme state")
	}
}
