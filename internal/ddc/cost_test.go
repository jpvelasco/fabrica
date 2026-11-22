package ddc

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestCostResourcesZen(t *testing.T) {
	got := CostResources(naming.Default, config.DDCConfig{})
	// S3 + EC2 + Volume
	if len(got) != 3 {
		t.Fatalf("len = %d: %+v", len(got), got)
	}
}

func TestCostResourcesScylla(t *testing.T) {
	got := CostResources(naming.Default, config.DDCConfig{Backend: BackendScylla})
	if len(got) != 5 {
		t.Fatalf("len = %d: %+v", len(got), got)
	}
}
