package config

import (
	"fmt"
	"maps"
	"os"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

const (
	DefaultFile       = "fabrica.yaml"
	DefaultProvider   = "aws"
	DefaultAWSRegion  = "us-east-1"
	DefaultStateTable = "fabrica-state-lock"
)

// Path returns the config file selected by the explicit path/profile flags.
func Path(explicit, profile string) string {
	if explicit != "" || profile == "" {
		return explicit
	}
	return fmt.Sprintf("fabrica-%s.yaml", profile)
}

// Save writes the config to a YAML file at the given path.
// If path is empty, defaults to fabrica.yaml.
func (c *Config) Save(path string) error {
	if path == "" {
		path = DefaultFile
	}

	data, err := c.YAML()
	if err != nil {
		return err
	}
	// fabrica.yaml is non-secret project config (not credentials).
	if err := os.WriteFile(path, data, 0644); err != nil { // #nosec G306 -- project config file, not secrets
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// YAML renders the config in the on-disk schema used by fabrica.yaml.
func (c *Config) YAML() ([]byte, error) {
	out, err := yaml.Marshal(c.fileConfig())
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	return out, nil
}

type Config struct {
	Cloud       Cloud             `mapstructure:"cloud"       yaml:"cloud"`
	State       State             `mapstructure:"state"       yaml:"state"`
	Perforce    PerforceConfig    `mapstructure:"perforce"    yaml:"perforce"`
	Horde       HordeConfig       `mapstructure:"horde"       yaml:"horde"`
	Lore        LoreConfig        `mapstructure:"lore"        yaml:"lore"`
	Workstation WorkstationConfig `mapstructure:"workstation" yaml:"workstation"`
	CI          CIConfig          `mapstructure:"ci"          yaml:"ci"`
	Deploy      DeployConfig      `mapstructure:"deploy"      yaml:"deploy"`
	DDC         DDCConfig         `mapstructure:"ddc"         yaml:"ddc"`
	Cost        CostConfig        `mapstructure:"cost"        yaml:"cost"`
}

// PerforceConfig holds the perforce: section of fabrica.yaml.
type PerforceConfig struct {
	Version      string               `mapstructure:"version"      yaml:"version"`
	InstanceType string               `mapstructure:"instanceType" yaml:"instanceType"`
	VolumeSize   int                  `mapstructure:"volumeSize"   yaml:"volumeSize"`
	VPCId        string               `mapstructure:"vpcId"        yaml:"vpcId"`
	SubnetId     string               `mapstructure:"subnetId"     yaml:"subnetId"`
	AllowedCIDR  string               `mapstructure:"allowedCidr"  yaml:"allowedCidr"`
	Backup       PerforceBackupConfig `mapstructure:"backup"       yaml:"backup"`
}

// PerforceBackupConfig holds the perforce.backup: section of fabrica.yaml.
// Defaults are applied in the perforce plan layer (path, S3 prefix).
type PerforceBackupConfig struct {
	Path     string `mapstructure:"path"     yaml:"path"`
	S3Export bool   `mapstructure:"s3Export" yaml:"s3Export"`
	S3Bucket string `mapstructure:"s3Bucket" yaml:"s3Bucket"`
	S3Prefix string `mapstructure:"s3Prefix" yaml:"s3Prefix"`
}

// HordeConfig holds the horde: section of fabrica.yaml.
type HordeConfig struct {
	AmiID        string `mapstructure:"amiId"        yaml:"amiId"`
	InstanceType string `mapstructure:"instanceType" yaml:"instanceType"`
	VolumeSize   int    `mapstructure:"volumeSize"   yaml:"volumeSize"`
	VPCId        string `mapstructure:"vpcId"        yaml:"vpcId"`
	SubnetId     string `mapstructure:"subnetId"     yaml:"subnetId"`
	Port         int    `mapstructure:"port"         yaml:"port"`
	GRPCPort     int    `mapstructure:"grpcPort"     yaml:"grpcPort"`
	AllowedCIDR  string `mapstructure:"allowedCidr"  yaml:"allowedCidr"`
}

// LoreConfig holds the lore: section of fabrica.yaml.
// AMI-first: lore.amiId must point at an AMI that already contains loreserver.
type LoreConfig struct {
	AmiID        string `mapstructure:"amiId"        yaml:"amiId"`
	InstanceType string `mapstructure:"instanceType" yaml:"instanceType"`
	VolumeSize   int    `mapstructure:"volumeSize"   yaml:"volumeSize"`
	VPCId        string `mapstructure:"vpcId"        yaml:"vpcId"`
	SubnetId     string `mapstructure:"subnetId"     yaml:"subnetId"`
	AllowedCIDR  string `mapstructure:"allowedCidr"  yaml:"allowedCidr"`
}

// CIConfig holds the ci: section of fabrica.yaml. Defaults are applied in the
// ci plan layer (internal/ci), matching the Horde/Workstation pattern.
type CIConfig struct {
	ProjectName  string `mapstructure:"projectName"  yaml:"projectName"`
	ComputeType  string `mapstructure:"computeType"  yaml:"computeType"`
	Image        string `mapstructure:"image"        yaml:"image"`
	BuildTimeout int    `mapstructure:"buildTimeout" yaml:"buildTimeout"`
}

// DeployConfig holds the deploy: section of fabrica.yaml. Defaults are applied
// in the deploy plan layer (internal/deploy), matching the CI/Horde pattern.
type DeployConfig struct {
	AliasName                string `mapstructure:"aliasName"                yaml:"aliasName"`
	RoleName                 string `mapstructure:"roleName"                 yaml:"roleName"`
	FleetName                string `mapstructure:"fleetName"                yaml:"fleetName"`
	InstanceType             string `mapstructure:"instanceType"             yaml:"instanceType"`
	FleetType                string `mapstructure:"fleetType"                yaml:"fleetType"`
	LaunchPath               string `mapstructure:"launchPath"               yaml:"launchPath"`
	BuildBucket              string `mapstructure:"buildBucket"              yaml:"buildBucket"`
	BuildOS                  string `mapstructure:"buildOs"                  yaml:"buildOs"`
	FromPort                 int    `mapstructure:"fromPort"                 yaml:"fromPort"`
	ToPort                   int    `mapstructure:"toPort"                   yaml:"toPort"`
	DesiredInstances         int    `mapstructure:"desiredInstances"         yaml:"desiredInstances"`
	ActivationTimeoutMinutes int    `mapstructure:"activationTimeoutMinutes" yaml:"activationTimeoutMinutes"`
}

// DDCConfig holds the ddc: section of fabrica.yaml.
// AMI-first Unreal Cloud DDC (Jupiter / Zen). Single home-region in V1.
type DDCConfig struct {
	Backend            string `mapstructure:"backend"            yaml:"backend"` // zen|scylla; default zen
	AmiID              string `mapstructure:"amiId"              yaml:"amiId"`
	ScyllaAmiID        string `mapstructure:"scyllaAmiId"        yaml:"scyllaAmiId"`
	InstanceType       string `mapstructure:"instanceType"       yaml:"instanceType"`
	VolumeSize         int    `mapstructure:"volumeSize"         yaml:"volumeSize"`
	ScyllaInstanceType string `mapstructure:"scyllaInstanceType" yaml:"scyllaInstanceType"`
	ScyllaVolumeSize   int    `mapstructure:"scyllaVolumeSize"   yaml:"scyllaVolumeSize"`
	VPCId              string `mapstructure:"vpcId"              yaml:"vpcId"`
	SubnetId           string `mapstructure:"subnetId"           yaml:"subnetId"`
	AllowedCIDR        string `mapstructure:"allowedCidr"        yaml:"allowedCidr"`
	InternalCIDR       string `mapstructure:"internalCidr"       yaml:"internalCidr"`
	PublicPort         int    `mapstructure:"publicPort"         yaml:"publicPort"`
	InternalPort       int    `mapstructure:"internalPort"       yaml:"internalPort"`
	Bucket             string `mapstructure:"bucket"             yaml:"bucket"`
	Namespace          string `mapstructure:"namespace"          yaml:"namespace"`
}

// WorkstationConfig holds the workstation: section of fabrica.yaml.
type WorkstationConfig struct {
	AmiID              string `mapstructure:"amiId"              yaml:"amiId"`
	InstanceType       string `mapstructure:"instanceType"       yaml:"instanceType"`
	VolumeSize         int    `mapstructure:"volumeSize"         yaml:"volumeSize"`
	VPCId              string `mapstructure:"vpcId"              yaml:"vpcId"`
	SubnetId           string `mapstructure:"subnetId"           yaml:"subnetId"`
	IdleTimeoutMinutes int    `mapstructure:"idleTimeoutMinutes" yaml:"idleTimeoutMinutes"`
	AllowedCIDR        string `mapstructure:"allowedCidr"        yaml:"allowedCidr"`
}

// CostConfig holds the cost: section of fabrica.yaml.
type CostConfig struct {
	Budgets []BudgetThreshold `mapstructure:"budgets" yaml:"budgets"`
}

// BudgetThreshold is a single local budget guardrail. Scope is "total" or a
// module name; Monthly is the USD/month ceiling; WarnPct is the warn threshold
// as a percent of Monthly (0 → engine default of 80).
type BudgetThreshold struct {
	Scope   string  `mapstructure:"scope"   yaml:"scope"`
	Monthly float64 `mapstructure:"monthly" yaml:"monthly"`
	WarnPct int     `mapstructure:"warnPct" yaml:"warnPct,omitempty"`
}

type Cloud struct {
	Provider string `mapstructure:"provider" yaml:"provider"`
	AWS      AWS    `mapstructure:"aws" yaml:"aws"`
}

type AWS struct {
	Region    string            `mapstructure:"region" yaml:"region"`
	Profile   string            `mapstructure:"profile" yaml:"profile"`
	AccountID string            `mapstructure:"accountId" yaml:"accountId"`
	Tags      map[string]string `mapstructure:"tags" yaml:"tags"`
}

type State struct {
	Bucket   string `mapstructure:"bucket" yaml:"bucket"`
	Table    string `mapstructure:"table" yaml:"table"`
	KMSKeyID string `mapstructure:"kmsKeyId" yaml:"kmsKeyId"`
}

type fileConfig struct {
	Cloud       Cloud             `yaml:"cloud"`
	State       State             `yaml:"state"`
	Perforce    PerforceConfig    `yaml:"perforce"`
	Horde       HordeConfig       `yaml:"horde"`
	Lore        LoreConfig        `yaml:"lore"`
	Workstation WorkstationConfig `yaml:"workstation"`
	CI          CIConfig          `yaml:"ci"`
	Deploy      DeployConfig      `yaml:"deploy"`
	DDC         DDCConfig         `yaml:"ddc"`
	Cost        CostConfig        `yaml:"cost"`
}

func (c *Config) fileConfig() fileConfig {
	return fileConfig{
		Cloud:       c.Cloud,
		State:       c.State,
		Perforce:    c.Perforce,
		Horde:       c.Horde,
		Lore:        c.Lore,
		Workstation: c.Workstation,
		CI:          c.CI,
		Deploy:      c.Deploy,
		DDC:         c.DDC,
		Cost:        c.Cost,
	}
}

// Defaults returns a pre-populated Config with sensible defaults.
func Defaults() *Config {
	return &Config{
		Cloud: Cloud{
			Provider: DefaultProvider,
			AWS: AWS{
				Region: DefaultAWSRegion,
				Tags:   make(map[string]string),
			},
		},
		State: State{
			Table: DefaultStateTable,
		},
	}
}

// Clone returns a deep copy of the config.
func (c *Config) Clone() *Config {
	out := *c
	out.Cloud.AWS.Tags = make(map[string]string, len(c.Cloud.AWS.Tags))
	maps.Copy(out.Cloud.AWS.Tags, c.Cloud.AWS.Tags)
	return &out
}

// Load reads configuration from the given YAML file path, merges with defaults,
// and returns a fully populated Config. If path is empty, it searches for
// fabrica.yaml in the current directory.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	v := viper.New()
	v.SetConfigType("yaml")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigFile(DefaultFile)
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return cfg, nil
		}
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	cfg.normalize()

	return cfg, nil
}

func (c *Config) normalize() {
	if c.Cloud.Provider == "" {
		c.Cloud.Provider = DefaultProvider
	}
	if c.Cloud.AWS.Region == "" {
		c.Cloud.AWS.Region = DefaultAWSRegion
	}
	if c.Cloud.AWS.Tags == nil {
		c.Cloud.AWS.Tags = make(map[string]string)
	}
	if c.State.Table == "" {
		c.State.Table = DefaultStateTable
	}
}
