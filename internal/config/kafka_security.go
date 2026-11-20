package config

import (
	"errors"
	"os"
	"strings"
)

const (
	KafkaPlaintext = "PLAINTEXT"
	KafkaSSL       = "SSL"
	KafkaSASLSSL   = "SASL_SSL"
	KafkaSCRAM256  = "SCRAM-SHA-256"
	KafkaSCRAM512  = "SCRAM-SHA-512"
)

type KafkaSecurityConfig struct {
	Protocol     string `yaml:"protocol"`
	CAFile       string `yaml:"ca_file"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	Mechanism    string `yaml:"mechanism"`
	UsernameFile string `yaml:"username_file"`
	PasswordFile string `yaml:"password_file"`
}

func (c KafkaSecurityConfig) SecurityProtocol() string {
	return strings.ToUpper(strings.TrimSpace(c.Protocol))
}

func (c KafkaSecurityConfig) Validate() error {
	hasCert := c.CertFile != "" || c.KeyFile != ""
	hasSASL := c.Mechanism != "" || c.UsernameFile != "" || c.PasswordFile != ""
	switch c.SecurityProtocol() {
	case "", KafkaPlaintext:
		if c.CAFile != "" || hasCert || hasSASL {
			return errors.New("kafka credentials and CA require SSL or SASL_SSL")
		}
	case KafkaSSL:
		if c.CertFile == "" || c.KeyFile == "" || hasSASL {
			return errors.New("kafka SSL requires client certificate and key files only")
		}
	case KafkaSASLSSL:
		if hasCert {
			return errors.New("kafka SASL_SSL does not use client certificate files")
		}
		if c.Mechanism != KafkaSCRAM256 && c.Mechanism != KafkaSCRAM512 {
			return errors.New("kafka SASL_SSL requires SCRAM-SHA-256 or SCRAM-SHA-512")
		}
		if c.UsernameFile == "" || c.PasswordFile == "" {
			return errors.New("kafka SASL_SSL requires username and password files")
		}
	default:
		return errors.New("kafka security protocol must be PLAINTEXT, SSL or SASL_SSL")
	}
	return nil
}

func (c KafkaSecurityConfig) validateEnvironment(environment string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "local", "dev", "development", "test":
		return nil
	default:
		if protocol := c.SecurityProtocol(); protocol == "" || protocol == KafkaPlaintext {
			return errors.New("kafka requires SSL or SASL_SSL outside local, dev or test")
		}
		return nil
	}
}

func (c *KafkaSecurityConfig) applyEnv() {
	for _, setting := range []struct {
		name string
		dest *string
	}{
		{"SANDBOX_KAFKA_SECURITY_PROTOCOL", &c.Protocol},
		{"SANDBOX_KAFKA_TLS_CA_FILE", &c.CAFile},
		{"SANDBOX_KAFKA_TLS_CERT_FILE", &c.CertFile},
		{"SANDBOX_KAFKA_TLS_KEY_FILE", &c.KeyFile},
		{"SANDBOX_KAFKA_SASL_MECHANISM", &c.Mechanism},
		{"SANDBOX_KAFKA_SASL_USERNAME_FILE", &c.UsernameFile},
		{"SANDBOX_KAFKA_SASL_PASSWORD_FILE", &c.PasswordFile},
	} {
		if value, ok := os.LookupEnv(setting.name); ok {
			*setting.dest = strings.TrimSpace(value)
		}
	}
}
