package horde

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the Horde cloud-init script.
type UserDataConfig struct {
	MongoPassword string
	Port          int
	GRPCPort      int
}

var userDataRenderer = userdata.New(template.Must(template.New("horde-userdata").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-init.log) 2>&1

# Wait for MongoDB to be healthy (may be starting from AMI service)
for i in $(seq 1 12); do
  mongosh --eval "db.adminCommand('ping')" --quiet && break
  [ $i -eq 12 ] && echo "ERROR: MongoDB did not become healthy within 60s" && exit 1
  sleep 5
done

# Create horde database user (idempotent)
mongosh admin --eval "
  if (!db.getUser('horde')) {
    db.createUser({
      user: 'horde',
      pwd: '{{ .MongoPassword }}',
      roles: [{ role: 'readWrite', db: 'Horde' }]
    });
  }
"

# Write Horde Server.json
mkdir -p /etc/horde
cat > /etc/horde/Server.json <<'HORDEEOF'
{
  "Horde": {
    "DatabaseConnectionString": "mongodb://horde:{{ .MongoPassword }}@localhost:27017/Horde?authSource=admin&readPreference=primary",
    "RedisConnectionConfig": "127.0.0.1:6379",
    "HttpPort": {{ .Port }},
    "Http2Port": {{ .GRPCPort }}
  }
}
HORDEEOF

# Start services in dependency order
systemctl restart redis-server || systemctl restart redis
systemctl restart horde

touch /var/lib/cloud/instance/horde-ready
`)))

// validate checks required fields. Returns nil if valid.
func (cfg *UserDataConfig) validate() error {
	if cfg.MongoPassword == "" {
		return fmt.Errorf("MongoPassword must not be empty")
	}
	return nil
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}
	return userDataRenderer.Render(cfg)
}

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}
	return userDataRenderer.RenderBase64(cfg)
}
