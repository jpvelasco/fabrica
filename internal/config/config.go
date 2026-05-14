package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	Cloud    Cloud    `mapstructure:"cloud"`
	State    State    `mapstructure:"state"`
	Perforce any     `mapstructure:"perforce"`
	Horde    any     `mapstructure:"horde"`
	CI       any     `mapstructure:"ci"`
	Cost     any     `mapstructure:"cost"`
}

type Cloud struct {
	Provider string `mapstructure:"provider"`
	AWS      AWS    `mapstructure:"aws"`
}

type AWS struct {
	Region    string            `mapstructure:"region"`
	Profile   string            `mapstructure:"profile"`
	AccountID string            `mapstructure:"accountId"`
	Tags      map[string]string `mapstructure:"tags"`
}

type State struct {
	Bucket   string `mapstructure:"bucket"`
	Table    string `mapstructure:"table"`
	KMSKeyID string `mapstructure:"kmsKeyId"`
}

// Defaults returns a pre-populated Config with sensible defaults.
func Defaults() *Config {
	return &Config{
		Cloud: Cloud{
			Provider: "aws",
			AWS: AWS{
				Region: "us-east-1",
				Tags:   make(map[string]string),
			},
		},
		State: State{
			Table: "fabrica-state-lock",
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
		v.SetConfigFile("fabrica.yaml")
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

	return cfg, nil
}
