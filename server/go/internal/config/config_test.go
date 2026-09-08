package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validSecret = "0123456789abcdef0123456789abcdef"

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AAGASA_POSTGRES_URL", "postgres://user:pass@localhost:5432/aagasa")
	t.Setenv("AAGASA_REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("AAGASA_MONGO_URL", "mongodb://localhost:27017")
	t.Setenv("AAGASA_WORKER_SHARED_SECRET", validSecret)
}

func TestLoadAppliesDefaults(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddress != ":8080" {
		t.Errorf("HTTPAddress = %q, want :8080", cfg.HTTPAddress)
	}
	if cfg.GRPCAddress != ":9090" {
		t.Errorf("GRPCAddress = %q, want :9090", cfg.GRPCAddress)
	}
	if cfg.MongoDB != "aagasa" {
		t.Errorf("MongoDB = %q, want aagasa", cfg.MongoDB)
	}
}

// Starting without a datastore URL must fail loudly rather than silently
// running against a default.
func TestLoadRequiresDatastoreURLs(t *testing.T) {
	for _, name := range []string{"AAGASA_POSTGRES_URL", "AAGASA_REDIS_URL", "AAGASA_MONGO_URL"} {
		t.Run(name, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv(name, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error when %s is empty", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name %s", err, name)
			}
		})
	}
}

func TestLoadRejectsWeakWorkerSecret(t *testing.T) {
	for _, secret := range []string{"", "short", strings.Repeat("a", 31)} {
		setValidEnv(t)
		t.Setenv("AAGASA_WORKER_SHARED_SECRET", secret)

		if _, err := Load(); err == nil {
			t.Errorf("expected error for worker secret of length %d", len(secret))
		}
	}
}

func TestLoadRejectsInvalidStartupTimeout(t *testing.T) {
	for _, value := range []string{"abc", "0", "-5"} {
		setValidEnv(t)
		t.Setenv("AAGASA_STARTUP_TIMEOUT", value)

		if _, err := Load(); err == nil {
			t.Errorf("expected error for AAGASA_STARTUP_TIMEOUT=%q", value)
		}
	}
}

// TLS -----------------------------------------------------------------------
//
// The Worker presents a shared secret on every gRPC call, so the link between
// them must be encrypted on any untrusted network (spec.md section 21).

func writeCertificate(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "aagasa-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	directory := t.TempDir()
	certFile = filepath.Join(directory, "tls.crt")
	keyFile = filepath.Join(directory, "tls.key")

	if err := os.WriteFile(certFile,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile,
		pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func TestTLSIsOffByDefault(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TLSEnabled() {
		t.Error("TLS is on without being configured")
	}
}

func TestTLSMaterialIsAccepted(t *testing.T) {
	setValidEnv(t)
	certFile, keyFile := writeCertificate(t)
	t.Setenv("AAGASA_TLS_CERT_FILE", certFile)
	t.Setenv("AAGASA_TLS_KEY_FILE", keyFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.TLSEnabled() {
		t.Error("TLS is off with both files configured")
	}
}

// Half a TLS configuration is a misconfiguration, not an instruction to serve
// plaintext.
func TestHalfATLSConfigurationIsRefused(t *testing.T) {
	certFile, keyFile := writeCertificate(t)

	for name, pair := range map[string][2]string{
		"only the certificate": {certFile, ""},
		"only the key":         {"", keyFile},
	} {
		t.Run(name, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv("AAGASA_TLS_CERT_FILE", pair[0])
			t.Setenv("AAGASA_TLS_KEY_FILE", pair[1])

			_, err := Load()
			if err == nil {
				t.Fatal("a half configuration was accepted")
			}
			if !strings.Contains(err.Error(), "must be set together") {
				t.Errorf("error = %v", err)
			}
		})
	}
}

// A path that is not there is worth failing on at startup rather than at the
// first connection.
func TestUnreadableTLSMaterialIsRefused(t *testing.T) {
	setValidEnv(t)
	t.Setenv("AAGASA_TLS_CERT_FILE", filepath.Join(t.TempDir(), "absent.crt"))
	t.Setenv("AAGASA_TLS_KEY_FILE", filepath.Join(t.TempDir(), "absent.key"))

	_, err := Load()
	if err == nil {
		t.Fatal("missing TLS material was accepted")
	}
	if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("error = %v", err)
	}
}
