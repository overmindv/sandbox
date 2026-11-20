package gojudge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"overmindv/sandbox/internal/config"
)

type tlsIssuer struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
}

func newTLSIssuer(t testing.TB, name string) tlsIssuer {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tlsIssuer{cert: certificate, key: private}
}

func (i tlsIssuer) leaf(t testing.TB, usage x509.ExtKeyUsage, ip net.IP) tls.Certificate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	if ip != nil {
		template.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, i.cert, public, i.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}
}

func localMTLS(t testing.TB, serverCA, clientCA tlsIssuer, address net.IP, handler http.Handler) *httptest.Server {
	t.Helper()
	trustedClients := x509.NewCertPool()
	trustedClients.AddCert(clientCA.cert)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCA.leaf(t, x509.ExtKeyUsageServerAuth, address)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    trustedClients,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func clientTLSFiles(t testing.TB, trust tlsIssuer, client tls.Certificate) config.GoJudgeTLS {
	t.Helper()
	key, err := x509.MarshalPKCS8PrivateKey(client.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := config.GoJudgeTLS{
		CAFile:   filepath.Join(dir, "ca.pem"),
		CertFile: filepath.Join(dir, "client.pem"),
		KeyFile:  filepath.Join(dir, "client-key.pem"),
	}
	for _, item := range []struct {
		path, kind string
		data       []byte
	}{
		{files.CAFile, "CERTIFICATE", trust.cert.Raw},
		{files.CertFile, "CERTIFICATE", client.Certificate[0]},
		{files.KeyFile, "PRIVATE KEY", key},
	} {
		if err := os.WriteFile(item.path, pem.EncodeToMemory(&pem.Block{Type: item.kind, Bytes: item.data}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestRunnerMutualTLS(t *testing.T) {
	runnerCA := newTLSIssuer(t, "runner CA")
	workerCA := newTLSIssuer(t, "worker CA")
	untrustedCA := newTLSIssuer(t, "untrusted CA")
	worker := workerCA.leaf(t, x509.ExtKeyUsageClientAuth, nil)
	untrustedWorker := untrustedCA.leaf(t, x509.ExtKeyUsageClientAuth, nil)
	job := Request{Cmd: []Command{{Args: []string{"fixture"}}}}
	for _, tc := range []struct {
		name    string
		trust   tlsIssuer
		client  tls.Certificate
		address net.IP
		allowed bool
	}{
		{"trusted worker", runnerCA, worker, net.IPv4(127, 0, 0, 1), true},
		{"unknown runner CA", untrustedCA, worker, net.IPv4(127, 0, 0, 1), false},
		{"wrong runner address", runnerCA, worker, nil, false},
		{"unknown worker CA", runnerCA, untrustedWorker, net.IPv4(127, 0, 0, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := localMTLS(t, runnerCA, workerCA, tc.address, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
					t.Error("request reached runner without a verified client certificate")
				}
				switch r.URL.Path {
				case "/version":
					w.WriteHeader(http.StatusOK)
				case "/run":
					_, _ = w.Write([]byte(`[{"status":"Accepted","exitStatus":0,"files":{"stdout":"","stderr":""}}]`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			client, err := NewClient(config.GoJudgeClient{BaseURL: server.URL, TLS: clientTLSFiles(t, tc.trust, tc.client)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			healthy := client.Healthy(ctx)
			results, err := client.Run(ctx, job)
			if tc.allowed {
				if !healthy || err != nil || len(results) != 1 || calls.Load() != 2 {
					t.Errorf("mutual TLS failed: healthy=%v calls=%d err=%v", healthy, calls.Load(), err)
				}
			} else if healthy || !errors.Is(err, ErrInfrastructure) || calls.Load() != 0 {
				t.Errorf("TLS failure reached runner: healthy=%v calls=%d err=%v", healthy, calls.Load(), err)
			}
		})
	}
}

func TestRunnerTLSRejectsUnsafeConfiguration(t *testing.T) {
	trust := newTLSIssuer(t, "test CA")
	client := trust.leaf(t, x509.ExtKeyUsageClientAuth, nil)
	files := clientTLSFiles(t, trust, client)
	for _, tc := range []struct {
		name, endpoint, message string
		tls                     config.GoJudgeTLS
	}{
		{"plain HTTP", "http://runner.test", "requires https", files},
		{"missing key", "https://runner.test", "requires ca_file", config.GoJudgeTLS{
			CAFile: files.CAFile, CertFile: files.CertFile,
		}},
		{"invalid CA file", "https://runner.test", "no PEM certificates", config.GoJudgeTLS{
			CAFile: files.KeyFile, CertFile: files.CertFile, KeyFile: files.KeyFile,
		}},
		{"invalid key", "https://runner.test", "cannot load", config.GoJudgeTLS{
			CAFile: files.CAFile, CertFile: files.CertFile, KeyFile: files.CertFile,
		}},
		{"URL user info", "https://worker:fixture-marker@runner.test", "cannot contain credentials", files},
		{"URL query", "https://runner.test?token=fixture-marker", "cannot contain credentials", files},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(config.GoJudgeClient{BaseURL: tc.endpoint, TLS: tc.tls})
			if err == nil || !strings.Contains(err.Error(), tc.message) || strings.Contains(err.Error(), "fixture-marker") {
				t.Error("client did not reject the configuration with a redacted error")
			}
		})
	}
}

func TestRunnerDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, err := NewClient(config.GoJudgeClient{BaseURL: redirect.URL})
	if err != nil {
		t.Fatal(err)
	}
	healthy := client.Healthy(context.Background())
	_, err = client.Run(context.Background(), Request{Cmd: []Command{{Args: []string{"fixture"}}}})
	if healthy || !errors.Is(err, ErrRequestFailed) || redirected.Load() != 0 {
		t.Errorf("runner redirect was followed: healthy=%v requests=%d err=%v", healthy, redirected.Load(), err)
	}
}

func BenchmarkRunnerHealth(b *testing.B) {
	serve := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	b.Run("HTTP", func(b *testing.B) {
		server := httptest.NewServer(serve)
		b.Cleanup(server.Close)
		benchmarkHealth(b, config.GoJudgeClient{BaseURL: server.URL})
	})
	b.Run("mTLS", func(b *testing.B) {
		runnerCA := newTLSIssuer(b, "runner CA")
		workerCA := newTLSIssuer(b, "worker CA")
		server := localMTLS(b, runnerCA, workerCA, net.IPv4(127, 0, 0, 1), serve)
		worker := workerCA.leaf(b, x509.ExtKeyUsageClientAuth, nil)
		benchmarkHealth(b, config.GoJudgeClient{BaseURL: server.URL, TLS: clientTLSFiles(b, runnerCA, worker)})
	})
}

func benchmarkHealth(b *testing.B, cfg config.GoJudgeClient) {
	b.Helper()
	client, err := NewClient(cfg)
	if err != nil {
		b.Fatal(err)
	}
	if !client.Healthy(context.Background()) {
		b.Fatal("runner did not pass initial health check")
	}
	b.ReportAllocs()
	for b.Loop() {
		if !client.Healthy(context.Background()) {
			b.Fatal("runner did not respond")
		}
	}
}
