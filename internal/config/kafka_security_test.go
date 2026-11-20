package config

import (
	"strings"
	"testing"
)

func TestKafkaSecurityEnvironment(t *testing.T) {
	validMTLS := KafkaSecurityConfig{Protocol: KafkaSSL, CertFile: "client.pem", KeyFile: "key.pem"}
	validSCRAM := KafkaSecurityConfig{
		Protocol: KafkaSASLSSL, Mechanism: KafkaSCRAM256,
		UsernameFile: "username", PasswordFile: "password",
	}
	tests := []struct {
		name, environment string
		security          KafkaSecurityConfig
		allowed           bool
	}{
		{"local defaults", "local", KafkaSecurityConfig{}, true},
		{"dev plaintext", "dev", KafkaSecurityConfig{Protocol: KafkaPlaintext}, true},
		{"staging plaintext", "staging", KafkaSecurityConfig{Protocol: KafkaPlaintext}, false},
		{"prod defaults", "prod", KafkaSecurityConfig{}, false},
		{"prod plaintext", " PRODUCTION ", KafkaSecurityConfig{Protocol: KafkaPlaintext}, false},
		{"misspelled environment", "prodution", KafkaSecurityConfig{}, false},
		{"prod mtls", "prod", validMTLS, true},
		{"prod scram", "production", validSCRAM, true},
		{"plaintext with credentials", "local", KafkaSecurityConfig{UsernameFile: "username"}, false},
		{"missing client key", "prod", KafkaSecurityConfig{Protocol: KafkaSSL, CertFile: "client.pem"}, false},
		{"sasl without tls", "prod", KafkaSecurityConfig{Protocol: "SASL_PLAINTEXT"}, false},
		{"unsupported mechanism", "prod", KafkaSecurityConfig{Protocol: KafkaSASLSSL, Mechanism: "PLAIN"}, false},
		{"missing password", "prod", KafkaSecurityConfig{Protocol: KafkaSASLSSL, Mechanism: KafkaSCRAM512, UsernameFile: "username"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.App.Environment = tc.environment
			cfg.Kafka.Security = tc.security
			err := cfg.Validate()
			if tc.allowed && err != nil {
				t.Error(err)
			}
			if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "kafka ")) {
				t.Error("invalid kafka profile was accepted", err)
			}
		})
	}
}

func TestKafkaSecurityEnvOverridesYAMLPaths(t *testing.T) {
	t.Setenv("SANDBOX_KAFKA_SECURITY_PROTOCOL", KafkaSASLSSL)
	t.Setenv("SANDBOX_KAFKA_TLS_CERT_FILE", "")
	t.Setenv("SANDBOX_KAFKA_TLS_KEY_FILE", "")
	t.Setenv("SANDBOX_KAFKA_SASL_MECHANISM", KafkaSCRAM512)
	t.Setenv("SANDBOX_KAFKA_SASL_USERNAME_FILE", "/secrets/username")
	t.Setenv("SANDBOX_KAFKA_SASL_PASSWORD_FILE", "/secrets/password")

	cfg := Default()
	cfg.App.Environment = "prod"
	cfg.Kafka.Security = KafkaSecurityConfig{
		Protocol: KafkaSSL, CAFile: "/secrets/ca.pem", CertFile: "/secrets/client.pem", KeyFile: "/secrets/key.pem",
	}
	if err := applyEnvOverrides(&cfg); err != nil {
		t.Fatal(err)
	}
	want := KafkaSecurityConfig{
		Protocol: KafkaSASLSSL, CAFile: "/secrets/ca.pem", Mechanism: KafkaSCRAM512,
		UsernameFile: "/secrets/username", PasswordFile: "/secrets/password",
	}
	if cfg.Kafka.Security != want {
		t.Error("incorrect kafka security precedence")
	}
	if err := cfg.Validate(); err != nil {
		t.Error(err)
	}
}
