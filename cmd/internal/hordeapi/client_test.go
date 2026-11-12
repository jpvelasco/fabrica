package hordeapi

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/internal/horde"
)

func TestClientTrustAndProbe(t *testing.T) {
	reply := ""
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("probe sent credentials")
		}
		_, _ = w.Write([]byte(reply))
	}))
	defer s.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(s.URL, "https://127.0.0.1:"))
	c := horde.Connection{TLS: true, ServerName: "example.com", Port: port}
	dir := t.TempDir()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), ca, 0600); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(c, filepath.Join(dir, "fabrica.yaml"), "ca.pem", "127.0.0.1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reply = `{"http":0,"https":` + strconv.Itoa(port) + `,"unencryptedHttp2":0}`
	if err := Probe(context.Background(), client, c, ""); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{`, `{"http":5000,"https":` + strconv.Itoa(port) + `,"unencryptedHttp2":0}`, `{"http":0,"https":1,"unencryptedHttp2":0}`, strings.Repeat("x", 4097)} {
		reply = body
		if err := Probe(context.Background(), client, c, ""); err == nil {
			t.Fatalf("accepted %s", body[:1])
		}
	}
	// Unknown roots and a wrong SAN must fail rather than skip verification.
	untrusted, _ := NewClient(c, "", "", "127.0.0.1", time.Second)
	if err := Probe(context.Background(), untrusted, c, ""); err == nil {
		t.Fatal("accepted unknown CA")
	}
	c.ServerName = "wrong.example"
	wrong, _ := NewClient(c, "", filepath.Join(dir, "ca.pem"), "127.0.0.1", time.Second)
	if err := Probe(context.Background(), wrong, c, ""); err == nil {
		t.Fatal("accepted wrong SAN")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Probe(ctx, client, c, ""); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestClientErrorsAndRedirect(t *testing.T) {
	c := horde.Connection{}
	for _, tc := range []struct {
		ca, ip  string
		timeout time.Duration
	}{
		{"", "", 0}, {"missing.pem", "", time.Second}, {"", "bad", time.Second},
	} {
		if _, err := NewClient(c, "", tc.ca, tc.ip, tc.timeout); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not PEM"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(c, "", bad, "", time.Second); err == nil {
		t.Fatal("bad PEM accepted")
	}
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer destination.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer s.Close()
	client, _ := NewClient(c, "", "", "", time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL, nil)
	req.Header.Set("Authorization", "ServiceAccount test")
	if _, err := client.Do(req); err == nil || reached {
		t.Fatalf("redirect followed: %v %v", err, reached)
	}
}

func TestExpiredLeafAndNonSuccess(t *testing.T) {
	seed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := *seed.Certificate()
	key := seed.TLS.Certificates[0].PrivateKey
	seed.Close()
	cert.NotBefore = time.Now().Add(-48 * time.Hour)
	cert.NotAfter = time.Now().Add(-24 * time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, &cert, &cert, cert.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	s.StartTLS()
	defer s.Close()
	file := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(strings.TrimPrefix(s.URL, "https://127.0.0.1:"))
	c := horde.Connection{TLS: true, ServerName: "example.com", Port: port}
	client, err := NewClient(c, "", file, "127.0.0.1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := Probe(context.Background(), client, c, ""); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatal("expired leaf accepted", err)
	}
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer s2.Close()
	port, _ = strconv.Atoi(strings.TrimPrefix(s2.URL, "http://127.0.0.1:"))
	c = horde.Connection{Port: port}
	client, _ = NewClient(c, "", "", "", time.Second)
	if err := Probe(context.Background(), client, c, "127.0.0.1"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal("HTTP failure accepted", err)
	}
}

func TestPinnedClientRefusesDifferentHostAndBoundsRequests(t *testing.T) {
	c := horde.Connection{TLS: true, ServerName: "example.com", Port: 443}
	client, err := NewClient(c, "", "", "127.0.0.1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://different.example/api/v1/server/ports", nil)
	if _, err := client.Do(req); err == nil || !strings.Contains(err.Error(), "unexpected target") {
		t.Fatalf("unexpected host reached: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	c = horde.Connection{Port: port}
	client, err = NewClient(c, "", "", "", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := Probe(context.Background(), client, c, "127.0.0.1"); err == nil || !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("request timeout not enforced: %v", err)
	}
}
