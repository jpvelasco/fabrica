package horde

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestRuntimeCertificateValidation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux host validator")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not installed")
	}
	for _, name := range []string{"valid", "literal paths", "missing CA", "bad key", "permissions", "wrong SAN", "base ports", "base restart", "conflicting env", "changed overlay"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			material := filepath.Join(dir, "tls")
			if err := os.Mkdir(material, 0700); err != nil {
				t.Fatal(err)
			}
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			dns := "horde.example.internal"
			if name == "wrong SAN" {
				dns = "wrong.example.internal"
			}
			cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: dns}, DNSNames: []string{dns}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			pk, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.HordeConfig{TLS: config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: filepath.Join(material, "cert.pem"), KeyPath: filepath.Join(material, "key.pem"), CAPath: filepath.Join(material, "ca.pem")}}
			if name == "literal paths" {
				cfg.TLS.CertPath = filepath.Join(material, `cert"$(touch INJECTION_SENTINEL).pem`)
			}
			for path, data := range map[string][]byte{cfg.TLS.CertPath: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cfg.TLS.CAPath: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cfg.TLS.KeyPath: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := ConnectionFromConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			overlay, _ := RuntimeOverlay(c)
			script, _ := RuntimeValidation(c)
			script = strings.ReplaceAll(script, "/etc/horde/", dir+"/")
			if name == "changed overlay" {
				overlay = strings.ReplaceAll(overlay, `"Horde__HttpPort": "0"`, `"Horde__HttpPort": "5000"`)
			}
			for path, data := range map[string]string{filepath.Join(dir, "fabrica-runtime.json"): overlay, filepath.Join(dir, "fabrica-runtime-contract"): "1\n"} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			base := `{"services":{"horde":{"environment":{"Horde__AuthMethod":"Horde","SECRET_TOKEN":"never-print-this"},"restart":"no"}}}`
			switch name {
			case "base ports":
				base = `{"services":{"horde":{"ports":[{"target":5002}],"restart":"no"}}}`
			case "base restart":
				base = `{"services":{"horde":{"restart":"unless-stopped"}}}`
			case "conflicting env":
				base = `{"services":{"horde":{"environment":{"HORDE__HTTPPORT":"5000"},"restart":"no"}}}`
			case "missing CA":
				if err := os.Remove(cfg.TLS.CAPath); err != nil {
					t.Fatal(err)
				}
			case "bad key":
				if err := os.WriteFile(cfg.TLS.KeyPath, []byte("invalid key"), 0600); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(cfg.TLS.KeyPath, 0644); err != nil { // #nosec G302 -- deliberately unsafe fixture verifies key-permission rejection
					t.Fatal(err)
				}
			}
			docker := filepath.Join(dir, "docker")
			if err := os.WriteFile(docker, []byte("#!/bin/sh\ncat <<'BASE'\n"+base+"\nBASE\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if (err == nil) != (name == "valid" || name == "literal paths") {
				t.Fatalf("%s: %v %s", name, err, out)
			}
			if strings.Contains(string(out), "never-print-this") {
				t.Fatal("base environment leaked")
			}
			if _, err := os.Stat(filepath.Join(dir, "INJECTION_SENTINEL")); !os.IsNotExist(err) {
				t.Fatal("certificate path executed shell substitution")
			}
		})
	}
}

func TestAdoptionVerifierSanitizesInspectedEnvironment(t *testing.T) {
	c, _ := ConnectionFromConfig(tlsConfig())
	script, err := AdoptionVerifier(c)
	if err != nil {
		t.Fatal(err)
	}
	syntaxCheck(t, script)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	overlay, _ := RuntimeOverlay(c)
	// Run the actual generated Python verifier with only its host I/O replaced.
	// The inspected environment includes a secret to prove it is never emitted.
	prelude := `import subprocess, os, json
from types import SimpleNamespace
service=json.loads(` + strconv.Quote(overlay) + `)["services"]["horde"]
container={"State":{"Running":True},"Config":{"Env":[k+"="+v for k,v in service["environment"].items()]+["SECRET_TOKEN=never-print-this"]},"HostConfig":{"PortBindings":{"5000/tcp":[{"HostPort":"5000","HostIp":"0.0.0.0"}]},"ExtraHosts":service["extra_hosts"]},"Mounts":[{"Source":service["volumes"][0]["source"],"Destination":"/fabrica-tls","RW":False}]}
os.path.isfile=lambda _: True
def fake_run(args, **kwargs):
    if args[0]=="bash": return SimpleNamespace(returncode=0,stdout=b"",stderr=b"")
    return SimpleNamespace(returncode=0,stdout=json.dumps([container]).encode(),stderr=b"SECRET_TOKEN=never-print-this")
subprocess.run=fake_run
`
	body := strings.TrimSuffix(strings.TrimPrefix(script, "python3 - <<'FABRICA_VERIFY'\n"), "FABRICA_VERIFY\n")
	for _, invalid := range []bool{false, true} {
		input := prelude
		if invalid {
			input += `container["Config"]["Env"]=["SECRET_TOKEN=never-print-this"]` + "\n"
		}
		out, err := exec.Command(python, "-c", input+body).CombinedOutput()
		if (err != nil) != invalid || strings.Contains(string(out), "never-print-this") {
			t.Fatalf("verification: %s %v", out, err)
		}
	}
	if _, err := AdoptionVerifier(Connection{}); err == nil {
		t.Fatal("HTTP adoption accepted")
	}
}

func TestListenerPreflightRejectsRedirectsAndProtocolFallback(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	c, _ := ConnectionFromConfig(tlsConfig())
	for _, tc := range []struct {
		name, report, code, protocol string
		ok                           bool
	}{
		{"valid", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "200", "200 2", true},
		{"redirect", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "302", "302 2", false},
		{"boolean", `{"http":false,"https":5000,"unencryptedHttp2":0}`, "200", "200 2", false},
		{"plaintext", `{"http":5000,"https":5000,"unencryptedHttp2":0}`, "200", "200 2", false},
		{"fallback", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "200", "200 1.1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `set -euo pipefail
curl() {
  case "$*" in
    *http_version*) printf '%s' "$TEST_PROTOCOL" ;;
    *) printf '%s\n%s' "$TEST_PORTS" "$TEST_CODE" ;;
  esac
}
CURL_OPTS=(--silent)
HORDE_URL=https://horde.example.internal:5000
` + ListenerCheckScript(c)
			cmd := exec.Command(bash, "-c", script)
			cmd.Env = append(os.Environ(), "TEST_PROTOCOL="+tc.protocol, "TEST_PORTS="+tc.report, "TEST_CODE="+tc.code)
			if out, err := cmd.CombinedOutput(); (err == nil) != tc.ok {
				t.Fatalf("preflight result: %v %s", err, out)
			}
		})
	}
}
