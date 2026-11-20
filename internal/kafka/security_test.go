package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"overmindv/sandbox/internal/config"
)

func TestKafkaClientsUseSameSecurity(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	key, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certFile := kafkaFixtureFile(t, dir, "certificate", certificate)
	keyFile := kafkaFixtureFile(t, dir, "key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	for _, tc := range []struct {
		name     string
		security config.KafkaSecurityConfig
	}{
		{"mtls", config.KafkaSecurityConfig{Protocol: config.KafkaSSL, CAFile: certFile, CertFile: certFile, KeyFile: keyFile}},
		{"scram", config.KafkaSecurityConfig{
			Protocol: config.KafkaSASLSSL, Mechanism: config.KafkaSCRAM512, CAFile: certFile,
			UsernameFile: kafkaFixtureFile(t, dir, "user", []byte("sandbox\r\n")),
			PasswordFile: kafkaFixtureFile(t, dir, "password", []byte(" test-value \n")),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default().Kafka
			cfg.Security = tc.security
			reader, writer, err := NewClients(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
				if err := writer.Close(); err != nil {
					t.Error(err)
				}
			})
			dialer := reader.reader.Config().Dialer
			if writer.transport == nil || writer.transport.TLS != dialer.TLS ||
				writer.transport.SASL != dialer.SASLMechanism || writer.transport.ClientID != dialer.ClientID {
				t.Fatal("reader and writer must use the same security and client ID")
			}
			if dialer.TLS.InsecureSkipVerify || dialer.TLS.ServerName != "" || dialer.TLS.RootCAs == nil ||
				dialer.TLS.MinVersion != tls.VersionTLS12 {
				t.Error("TLS does not enforce trust and broker name verification")
			}
			if tc.name == "mtls" && (len(dialer.TLS.Certificates) != 1 || dialer.SASLMechanism != nil) {
				t.Error("mTLS did not load client identity")
			}
			if tc.name == "scram" && (dialer.SASLMechanism == nil || dialer.SASLMechanism.Name() != config.KafkaSCRAM512) {
				t.Error("incorrect SCRAM mechanism")
			}
		})
	}
}

func TestKafkaSecurityRejectsUnreadableOrInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	marker := strings.Repeat("invalid-content-", 2)
	bad := kafkaFixtureFile(t, dir, "bad", []byte(marker))
	large := kafkaFixtureFile(t, dir, "oversized", []byte(strings.Repeat("x", maxKafkaPEMBytes+1)))
	for _, file := range []string{bad, large, dir, filepath.Join(dir, "missing")} {
		cfg := config.KafkaSecurityConfig{
			Protocol: config.KafkaSSL, CAFile: file, CertFile: bad, KeyFile: bad,
		}
		_, err := newConnectionSecurity(cfg)
		if err == nil || strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), dir) {
			t.Error("invalid or sensitive CA input was not rejected safely")
		}
	}
	cfg := config.KafkaSecurityConfig{Protocol: config.KafkaSSL, CertFile: bad, KeyFile: bad}
	if _, err := newConnectionSecurity(cfg); err == nil || strings.Contains(err.Error(), marker) {
		t.Error("invalid client certificate/key pair was not rejected safely")
	}
	multiline := kafkaFixtureFile(t, dir, "multiline", []byte("first\nsecond"))
	if _, err := readKafkaCredential(multiline, "SASL password"); err == nil {
		t.Error("multiline password accepted")
	}
	spaces := kafkaFixtureFile(t, dir, "spaces", []byte(" valid value \r\n"))
	value, err := readKafkaCredential(spaces, "SASL password")
	if err != nil || value != " valid value " {
		t.Error("credential reader changed spaces around the secret")
	}
}

func kafkaFixtureFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
