package state

import (
	"fmt"
	"strconv"
	"time"
)

// State is the canonical representation of provisioning state.
type State struct {
	Version string        `json:"version"`
	Account string        `json:"account"`
	Region  string        `json:"region"`
	Modules []ModuleState `json:"modules"`
	History []Operation   `json:"history"`
	Updated time.Time     `json:"updated"`
	Created time.Time     `json:"created"`
}

// ModuleState tracks the state of one provisioning module.
type ModuleState struct {
	Name      string           `json:"name"`
	Version   string           `json:"version"`
	Status    string           `json:"status"`
	Resources []ModuleResource `json:"resources"`
	Provision string           `json:"provision"`
}

// ModuleResource is a single resource tracked in state.
type ModuleResource struct {
	TypeName   string            `json:"typeName"`
	Identifier string            `json:"identifier"`
	Properties map[string]string `json:"properties"`
}

// Operation records a state change in the history log.
type Operation struct {
	Module string    `json:"module"`
	Action string    `json:"action"`
	Time   time.Time `json:"time"`
	Count  int       `json:"count"`
}

func NewState(account, region string) *State {
	now := time.Now().UTC()
	return &State{
		Version: "0.1",
		Account: account,
		Region:  region,
		Modules: make([]ModuleState, 0),
		History: make([]Operation, 0),
		Updated: now,
		Created: now,
	}
}

// UpsertModule adds or updates the state of a module.
func (s *State) UpsertModule(name, version, status string, resources []ModuleResource) {
	now := time.Now().UTC()
	for i, m := range s.Modules {
		if m.Name == name {
			s.Modules[i].Version = version
			s.Modules[i].Status = status
			s.Modules[i].Resources = resources
			s.Modules[i].Provision = now.Format(time.RFC3339)
			s.Updated = now
			return
		}
	}
	s.Modules = append(s.Modules, ModuleState{
		Name:      name,
		Version:   version,
		Status:    status,
		Resources: resources,
		Provision: now.Format(time.RFC3339),
	})
	s.Updated = now
}

// AddOp appends an operation to the history log.
func (s *State) AddOp(module, action string, count int) {
	now := time.Now().UTC()
	s.History = append(s.History, Operation{Module: module, Action: action, Time: now, Count: count})
	s.Updated = now
}

// GetModule returns the module state for the given name, or nil if absent.
func (s *State) GetModule(name string) *ModuleState {
	for i := range s.Modules {
		if s.Modules[i].Name == name {
			return &s.Modules[i]
		}
	}
	return nil
}

// GetModuleResource returns the first resource matching typeName within the
// named module, or (nil, false) if the module or resource is not found.
func (s *State) GetModuleResource(module, typeName string) (*ModuleResource, bool) {
	m := s.GetModule(module)
	if m == nil {
		return nil, false
	}
	for i := range m.Resources {
		if m.Resources[i].TypeName == typeName {
			return &m.Resources[i], true
		}
	}
	return nil, false
}

// ModuleCount returns the total number of resources recorded across all modules.
func (s *State) ModuleCount() int {
	n := 0
	for _, m := range s.Modules {
		n += len(m.Resources)
	}
	return n
}

// LockID builds a lock identifier for a given region and bucket.
func LockID(region, bucket string) string {
	return fmt.Sprintf("%s/%s", region, bucket)
}

func (s *State) String() string {
	return strconv.Itoa(s.ModuleCount())
}
