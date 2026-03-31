package config

import (
	"log"
	"os"
	"strconv"
)

// Config centraliza todas as variáveis de ambiente do billing-service.
// Todos os secrets são carregados via env vars — nunca hardcoded (R4).
type Config struct {
	Port        string
	DatabaseURL string
	RabbitMQURL string

	// NFS-e Salvador — Padrão ABRASF
	NFSeEnvironment  string  // "homologacao" | "producao"
	NFSeEndpointURL  string  // URL do webservice da prefeitura de Salvador
	NFSeProviderCNPJ string  // CNPJ do prestador (IIT)
	NFSeProviderIM   string  // Inscrição Municipal do prestador
	NFSeCertPath     string  // path do certificado A1 (.pfx)
	NFSeCertPassword string  // senha do certificado A1
	NFSeAliquota     float64 // alíquota ISS — padrão Salvador: 5%
	NFSeItemLista    string  // código do serviço na lista ABRASF (ex: "01.07")

	AuthServiceURL string
	ServiceToken   string

	ServiceName    string
	ServiceVersion string
}

// Load lê as variáveis de ambiente e retorna a configuração.
// Faz fatal log se DATABASE_URL não estiver presente.
func Load() *Config {
	aliquota := 5.0
	if raw := os.Getenv("NFSE_ALIQUOTA"); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			aliquota = v
		}
	}

	cfg := &Config{
		Port:        getEnv("PORT", "3030"),
		DatabaseURL: requireEnv("DATABASE_URL"),
		RabbitMQURL: requireEnv("RABBITMQ_URL"),

		NFSeEnvironment:  getEnv("NFSE_ENVIRONMENT", "homologacao"),
		NFSeEndpointURL:  getEnv("NFSE_ENDPOINT_URL", "https://homologacao.sefin.salvador.ba.gov.br/nfse-ws/services/NfseServiceV2"),
		NFSeProviderCNPJ: getEnv("NFSE_PROVIDER_CNPJ", ""),
		NFSeProviderIM:   getEnv("NFSE_PROVIDER_IM", ""),
		NFSeCertPath:     getEnv("NFSE_CERT_PATH", ""),
		NFSeCertPassword: getEnv("NFSE_CERT_PASSWORD", ""),
		NFSeAliquota:     aliquota,
		NFSeItemLista:    getEnv("NFSE_ITEM_LISTA", "01.07"),

		AuthServiceURL: getEnv("AUTH_SERVICE_URL", "http://auth-service:3010"),
		ServiceToken:   getEnv("SERVICE_TOKEN", ""),

		ServiceName:    getEnv("SERVICE_NAME", "billing-service"),
		ServiceVersion: getEnv("SERVICE_VERSION", "1.0.0"),
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("variável de ambiente obrigatória não definida: %s", key)
	}
	return v
}
