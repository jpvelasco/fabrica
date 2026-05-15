package config

import (
	"fmt"
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
	if err := os.WriteFile(path, data, 0644); err != nil {
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
	Cloud    Cloud `mapstructure:"cloud" yaml:"cloud"`
	State    State `mapstructure:"state" yaml:"state"`
	Perforce any   `mapstructure:"perforce" yaml:"perforce"`
	Horde    any   `mapstructure:"horde" yaml:"horde"`
	CI       any   `mapstructure:"ci" yaml:"ci"`
	Cost     any   `mapstructure:"cost" yaml:"cost"`
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
	Cloud    Cloud `yaml:"cloud"`
	State    State `yaml:"state"`
	Perforce any   `yaml:"perforce"`
	Horde    any   `yaml:"horde"`
	CI       any   `yaml:"ci"`
	Cost     any   `yaml:"cost"`
}

func (c *Config) fileConfig() fileConfig {
	return fileConfig{
		Cloud:    c.Cloud,
		State:    c.State,
		Perforce: emptySection(c.Perforce),
		Horde:    emptySection(c.Horde),
		CI:       emptySection(c.CI),
		Cost:     emptySection(c.Cost),
	}
}

func emptySection(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
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
	for k, v := range c.Cloud.AWS.Tags {
		out.Cloud.AWS.Tags[k] = v
	}
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
