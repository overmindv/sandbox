package gojudge

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"overmindv/sandbox/internal/config"
)

func loadTLS(cfg config.GoJudgeTLS) (*tls.Config, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, errors.New("cannot read go-judge TLS CA file")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("go-judge TLS CA file has no PEM certificates")
	}
	certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, errors.New("cannot load go-judge TLS client certificate and key")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
	}, nil
}
