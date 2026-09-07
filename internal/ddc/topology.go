package ddc

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
)

// TopologyPlan is the operator-facing production Scylla + replication document.
type TopologyPlan struct {
	Backend     string   `json:"backend"`
	ScyllaNodes int      `json:"scyllaNodes"`
	RF          int      `json:"replicationFactor"`
	Datacenter  string   `json:"datacenter,omitempty"`
	Peers       []string `json:"peers,omitempty"`
	Production  bool     `json:"production"`
	Note        string   `json:"note"`
}

// ResolveScyllaNodes returns configured Scylla node count (default 1).
func ResolveScyllaNodes(cfg config.DDCScyllaConfig) int {
	if cfg.Nodes <= 0 {
		return 1
	}
	return cfg.Nodes
}

// ResolveRF returns RF (default 1 for bootstrap, 3 when nodes>=3).
func ResolveRF(cfg config.DDCScyllaConfig, nodes int) int {
	if cfg.Replication > 0 {
		return cfg.Replication
	}
	if nodes >= 3 {
		return 3
	}
	return 1
}

// ValidateProductionTopology rejects impossible Scylla/replication shapes.
func ValidateProductionTopology(cfg config.DDCConfig) error {
	if normalizeBackend(cfg.Backend) != BackendScylla && cfg.Scylla.Nodes == 0 && !cfg.Replication.Enabled {
		return nil
	}
	nodes := ResolveScyllaNodes(cfg.Scylla)
	if nodes != 1 && nodes < 3 {
		return fmt.Errorf("ddc.scylla.nodes must be 1 (bootstrap) or >=3 (production RF=3); got %d", nodes)
	}
	rf := ResolveRF(cfg.Scylla, nodes)
	if rf > nodes {
		return fmt.Errorf("ddc.scylla.replication (%d) cannot exceed ddc.scylla.nodes (%d)", rf, nodes)
	}
	if cfg.Replication.Enabled {
		for i, p := range cfg.Replication.Peers {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("ddc.replication.peers[%d] is empty", i)
			}
		}
	}
	return nil
}

// BuildTopologyPlan documents the configured Scylla + peer overlay.
func BuildTopologyPlan(cfg config.DDCConfig) (TopologyPlan, error) {
	if err := ValidateProductionTopology(cfg); err != nil {
		return TopologyPlan{}, err
	}
	backend := normalizeBackend(cfg.Backend)
	nodes := ResolveScyllaNodes(cfg.Scylla)
	rf := ResolveRF(cfg.Scylla, nodes)
	prod := backend == BackendScylla && nodes >= 3
	note := "V1 still provisions one Scylla host. Production RF/node counts are documented for cost and destroy planning; additional nodes stay operator-provisioned."
	if cfg.Replication.Enabled {
		note += " Replication peers are written into fabrica.env; Fabrica does not open inter-region sockets."
	}
	return TopologyPlan{
		Backend:     backend,
		ScyllaNodes: nodes,
		RF:          rf,
		Datacenter:  cfg.Scylla.Datacenter,
		Peers:       append([]string(nil), cfg.Replication.Peers...),
		Production:  prod,
		Note:        note,
	}, nil
}
