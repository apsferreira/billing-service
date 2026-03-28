package nfse

import (
	"context"
	"fmt"
	"log"
	"time"
)

// NFSeClient encapsula a comunicação com o webservice de NFS-e da prefeitura de Salvador/BA.
// Implementa o Padrão ABRASF v2.04 via SOAP.
type NFSeClient struct {
	endpointURL  string
	environment  string // "homologacao" | "producao"
	providerCNPJ string
	providerIM   string
	certPath     string
	certPassword string
}

// NewNFSeClient cria um novo cliente NFS-e.
// certPath e certPassword são o certificado digital A1 (.pfx) do prestador.
func NewNFSeClient(endpointURL, environment, providerCNPJ, providerIM, certPath, certPassword string) *NFSeClient {
	return &NFSeClient{
		endpointURL:  endpointURL,
		environment:  environment,
		providerCNPJ: providerCNPJ,
		providerIM:   providerIM,
		certPath:     certPath,
		certPassword: certPassword,
	}
}

// EnviarRPS envia um Recibo Provisório de Serviços ao webservice da prefeitura
// e retorna o número e código de verificação da NFS-e emitida.
//
// TODO: implementar integração SOAP real com webservice da prefeitura de Salvador.
// Documentação oficial: https://sefin.salvador.ba.gov.br/NFS-e
// Padrão: ABRASF v2.04
// Endpoint homologação: https://homologacao.sefin.salvador.ba.gov.br/nfse-ws/services/NfseServiceV2
// Endpoint produção:    https://producao.sefin.salvador.ba.gov.br/nfse-ws/services/NfseServiceV2
//
// A integração real requer:
//   - Certificado digital A1 do prestador (emitido por AC credenciada ICP-Brasil)
//   - Assinatura XML com xmldsig usando o certificado
//   - Envio via SOAP/HTTPS
//   - Parsing da resposta SOAP (sucesso ou lista de erros ABRASF)
func (c *NFSeClient) EnviarRPS(ctx context.Context, rps *RPS) (*NFSeResponse, error) {
	if c.environment == "producao" {
		// Nunca simular em produção — retornar erro explícito até a integração estar implementada
		return nil, fmt.Errorf("integração SOAP com prefeitura de Salvador ainda não implementada — ambiente de produção requer certificado A1 e assinatura XML")
	}

	// STUB: simula emissão em homologação para desenvolvimento e testes
	log.Printf("[nfse-stub] EnviarRPS — prestador CNPJ=%s IM=%s tomador=%s valor=%.2f discriminacao=%q",
		c.providerCNPJ, c.providerIM, rps.Tomador.RazaoSocial, rps.ValorServicos, rps.Discriminacao)

	numero := fmt.Sprintf("STUB-%d", time.Now().UnixMilli())
	return &NFSeResponse{
		Numero:            numero,
		CodigoVerificacao: fmt.Sprintf("STUB-VER-%d", time.Now().UnixMilli()),
		XML:               fmt.Sprintf(`<CompNfse><Nfse><InfNfse><Numero>%s</Numero></InfNfse></Nfse></CompNfse>`, numero),
	}, nil
}

// CancelarNFSe cancela uma NFS-e já emitida.
//
// TODO: implementar cancelamento via SOAP (operação CancelarNfse do ABRASF).
// O cancelamento requer justificativa e é sujeito a prazo limite da prefeitura.
func (c *NFSeClient) CancelarNFSe(ctx context.Context, nfseNumber, motivo string) error {
	if c.environment == "producao" {
		return fmt.Errorf("cancelamento SOAP ainda não implementado — ambiente de produção")
	}

	log.Printf("[nfse-stub] CancelarNFSe — numero=%s motivo=%q", nfseNumber, motivo)
	return nil
}
