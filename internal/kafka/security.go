package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/scram"

	"overmindv/sandbox/internal/config"
)

const (
	maxKafkaPEMBytes        = 1 << 20
	maxKafkaCredentialBytes = 4096
)

type connectionSecurity struct {
	tls  *tls.Config
	sasl sasl.Mechanism
}

func NewClients(cfg config.KafkaConfig) (*KafkaConsumer, *KafkaProducer, error) {
	security, err := newConnectionSecurity(cfg.Security)
	if err != nil {
		return nil, nil, err
	}
	consumer, err := newConsumer(cfg, security)
	if err != nil {
		return nil, nil, fmt.Errorf("configure kafka reader: %w", err)
	}
	return consumer, newProducer(cfg, security), nil
}

func newConnectionSecurity(cfg config.KafkaSecurityConfig) (connectionSecurity, error) {
	if err := cfg.Validate(); err != nil {
		return connectionSecurity{}, err
	}
	if protocol := cfg.SecurityProtocol(); protocol == "" || protocol == config.KafkaPlaintext {
		return connectionSecurity{}, nil
	}

	// kafka-go sets ServerName from each broker address, including advertised addresses.
	out := connectionSecurity{tls: &tls.Config{MinVersion: tls.VersionTLS12}}
	if cfg.CAFile != "" {
		pem, err := readKafkaFile(cfg.CAFile, "CA", maxKafkaPEMBytes)
		if err != nil {
			return connectionSecurity{}, err
		}
		out.tls.RootCAs = x509.NewCertPool()
		if !out.tls.RootCAs.AppendCertsFromPEM(pem) {
			return connectionSecurity{}, errors.New("kafka CA file contains no valid PEM certificates")
		}
	}

	switch cfg.SecurityProtocol() {
	case config.KafkaSSL:
		certPEM, err := readKafkaFile(cfg.CertFile, "client certificate", maxKafkaPEMBytes)
		if err != nil {
			return connectionSecurity{}, err
		}
		keyPEM, err := readKafkaFile(cfg.KeyFile, "client key", maxKafkaPEMBytes)
		if err != nil {
			return connectionSecurity{}, err
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return connectionSecurity{}, errors.New("kafka client certificate and key do not form a valid pair")
		}
		out.tls.Certificates = []tls.Certificate{cert}
	case config.KafkaSASLSSL:
		username, err := readKafkaCredential(cfg.UsernameFile, "SASL username")
		if err != nil {
			return connectionSecurity{}, err
		}
		password, err := readKafkaCredential(cfg.PasswordFile, "SASL password")
		if err != nil {
			return connectionSecurity{}, err
		}
		algorithm := scram.SHA256
		if cfg.Mechanism == config.KafkaSCRAM512 {
			algorithm = scram.SHA512
		}
		out.sasl, err = scram.Mechanism(algorithm, username, password)
		if err != nil {
			return connectionSecurity{}, errors.New("kafka SCRAM credentials are invalid")
		}
	}
	return out, nil
}

func readKafkaFile(path, kind string, limit int64) ([]byte, error) {
	// Secret volumes use symlinks to regular files. Do not read pipes or devices.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot access kafka %s file", kind)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > limit {
		return nil, fmt.Errorf("kafka %s file must be a non-empty regular file of at most %d bytes", kind, limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open kafka %s file", kind)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, fmt.Errorf("cannot read bounded kafka %s file", kind)
	}
	return data, nil
}

func readKafkaCredential(path, kind string) (string, error) {
	data, err := readKafkaFile(path, kind, maxKafkaCredentialBytes)
	if err != nil {
		return "", err
	}
	value := string(data)
	if line, ok := strings.CutSuffix(value, "\n"); ok {
		value = strings.TrimSuffix(line, "\r")
	}
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("kafka %s file must contain one non-empty line", kind)
	}
	return value, nil
}
