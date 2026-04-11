package nfse

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// soapAction define as operações disponíveis no webservice NFS-e de Salvador/BA.
const (
	soapActionGerarNfse   = "GerarNfse"
	soapActionCancelarNfse = "CancelarNfse"
)

// NFSeClient encapsula a comunicação com o webservice de NFS-e da prefeitura de Salvador/BA.
// Implementa o Padrão ABRASF v2.04 via SOAP com mTLS (certificado A1).
type NFSeClient struct {
	endpointURL  string
	environment  string // "homologacao" | "producao"
	providerCNPJ string
	providerIM   string
	certPath     string
	certPassword string
	httpClient   *http.Client
}

// NewNFSeClient cria um novo cliente NFS-e.
//
// Se certPath estiver vazio, o cliente opera em modo stub (homologação sem certificado).
// Se certPath estiver preenchido, carrega o certificado A1 e configura mTLS.
func NewNFSeClient(endpointURL, environment, providerCNPJ, providerIM, certPath, certPassword string) (*NFSeClient, error) {
	c := &NFSeClient{
		endpointURL:  endpointURL,
		environment:  environment,
		providerCNPJ: providerCNPJ,
		providerIM:   providerIM,
		certPath:     certPath,
		certPassword: certPassword,
	}

	if certPath == "" {
		// Modo stub: HTTP client padrão sem mTLS (certificado A1 pendente)
		c.httpClient = &http.Client{Timeout: 30 * time.Second}
		slog.Info("nfse inicializado em modo stub — emissão real desabilitada até configurar certificado A1",
			slog.String("environment", environment),
		)
		return c, nil
	}

	bundle, err := LoadCertBundle(certPath, certPassword)
	if err != nil {
		return nil, fmt.Errorf("erro ao carregar certificado A1: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.TLSCert},
		MinVersion:   tls.VersionTLS12,
	}

	transport := &http.Transport{TLSClientConfig: tlsCfg}
	c.httpClient = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	slog.Info("nfse cliente inicializado com certificado A1",
		slog.String("cert_cn", bundle.X509Cert.Subject.CommonName),
		slog.String("environment", environment),
	)

	return c, nil
}

// EnviarRPS envia um Recibo Provisório de Serviços ao webservice da prefeitura de Salvador/BA
// e retorna o número e código de verificação da NFS-e emitida.
//
// Fluxo completo:
//  1. Monta XML do lote conforme ABRASF v2.04
//  2. Assina com XMLDSig (RSA-SHA1) usando certificado A1
//  3. Encapsula em envelope SOAP
//  4. Envia via HTTPS com mTLS
//  5. Parseia resposta SOAP
func (c *NFSeClient) EnviarRPS(ctx context.Context, rps *RPS) (*NFSeResponse, error) {
	if c.certPath == "" {
		return c.stubEnviarRPS(rps)
	}

	loteID := fmt.Sprintf("%d", time.Now().UnixMilli())

	xmlLote, err := BuildEnviarLoteRpsEnvio(rps, loteID)
	if err != nil {
		return nil, fmt.Errorf("erro ao montar XML do lote: %w", err)
	}

	bundle, err := LoadCertBundle(c.certPath, c.certPassword)
	if err != nil {
		return nil, fmt.Errorf("erro ao carregar certificado para assinatura: %w", err)
	}

	xmlAssinado, err := SignXML(xmlLote, bundle)
	if err != nil {
		return nil, fmt.Errorf("erro ao assinar XML: %w", err)
	}

	soapEnvelope := BuildSOAPEnvelope(xmlAssinado)

	respBody, err := c.doSOAPRequest(ctx, soapEnvelope, soapActionGerarNfse)
	if err != nil {
		return nil, fmt.Errorf("erro na requisição SOAP GerarNfse: %w", err)
	}

	return parseGerarNfseResponse(respBody)
}

// CancelarNFSe cancela uma NFS-e já emitida pelo webservice da prefeitura.
//
// motivo: código de cancelamento ABRASF
//   - 1 = Erro na emissão
//   - 2 = Serviço não prestado
//   - 4 = Duplicidade da nota
func (c *NFSeClient) CancelarNFSe(ctx context.Context, nfseNumber, motivo string) error {
	if c.certPath == "" {
		return c.stubCancelarNFSe(nfseNumber, motivo)
	}

	codigoMotivo := 1
	switch motivo {
	case "servico_nao_prestado":
		codigoMotivo = 2
	case "duplicidade":
		codigoMotivo = 4
	}

	xmlCancel := BuildCancelarNfseEnvio(
		c.providerCNPJ,
		c.providerIM,
		nfseNumber,
		fmt.Sprintf("%d", CodigoMunicipioSalvador),
		codigoMotivo,
	)

	bundle, err := LoadCertBundle(c.certPath, c.certPassword)
	if err != nil {
		return fmt.Errorf("erro ao carregar certificado para assinatura: %w", err)
	}

	xmlAssinado, err := SignXML(xmlCancel, bundle)
	if err != nil {
		return fmt.Errorf("erro ao assinar XML de cancelamento: %w", err)
	}

	soapEnvelope := BuildSOAPEnvelope(xmlAssinado)

	respBody, err := c.doSOAPRequest(ctx, soapEnvelope, soapActionCancelarNfse)
	if err != nil {
		return fmt.Errorf("erro na requisição SOAP CancelarNfse: %w", err)
	}

	return parseCancelarNfseResponse(respBody)
}

// doSOAPRequest executa uma requisição HTTP POST ao webservice SOAP.
// Não loga o body da requisição para evitar exposição de dados do tomador (R5).
func (c *NFSeClient) doSOAPRequest(ctx context.Context, envelope, soapAction string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL, strings.NewReader(envelope))
	if err != nil {
		return "", fmt.Errorf("erro ao criar requisição HTTP: %w", err)
	}

	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", soapAction)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro ao enviar requisição para prefeitura: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("erro ao ler resposta do webservice: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Não logar o body completo — pode conter dados sensíveis
		return "", fmt.Errorf("webservice retornou status HTTP %d", resp.StatusCode)
	}

	return string(body), nil
}

// --- Parsing das respostas SOAP ---

// soapFaultResponse representa uma falha SOAP.
type soapFaultResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Fault struct {
			FaultCode   string `xml:"faultcode"`
			FaultString string `xml:"faultstring"`
		} `xml:"Fault"`
	} `xml:"Body"`
}

// gerarNfseSOAPResponse representa a resposta de sucesso do GerarNfse.
type gerarNfseSOAPResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		GerarNfseResponse struct {
			OutputXML struct {
				GerarNfseResposta struct {
					ListaNfse struct {
						CompNfse struct {
							Nfse struct {
								InfNfse struct {
									Numero            string `xml:"Numero"`
									CodigoVerificacao string `xml:"CodigoVerificacao"`
								} `xml:"InfNfse"`
							} `xml:"Nfse"`
						} `xml:"CompNfse"`
					} `xml:"ListaNfse"`
					ListaMensagemRetorno struct {
						MensagemRetorno []struct {
							Codigo      string `xml:"Codigo"`
							Mensagem    string `xml:"Mensagem"`
							Correcao    string `xml:"Correcao"`
						} `xml:"MensagemRetorno"`
					} `xml:"ListaMensagemRetorno"`
				} `xml:"GerarNfseResposta"`
			} `xml:"outputXML"`
		} `xml:"GerarNfseResponse"`
	} `xml:"Body"`
}

// cancelarNfseSOAPResponse representa a resposta do CancelarNfse.
type cancelarNfseSOAPResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		CancelarNfseResponse struct {
			OutputXML struct {
				CancelarNfseResposta struct {
					RetCancelamento struct {
						NfseCancelamento struct {
							Confirmacao struct {
								Sucesso string `xml:"Sucesso"`
							} `xml:"Confirmacao"`
						} `xml:"NfseCancelamento"`
					} `xml:"RetCancelamento"`
					ListaMensagemRetorno struct {
						MensagemRetorno []struct {
							Codigo   string `xml:"Codigo"`
							Mensagem string `xml:"Mensagem"`
						} `xml:"MensagemRetorno"`
					} `xml:"ListaMensagemRetorno"`
				} `xml:"CancelarNfseResposta"`
			} `xml:"outputXML"`
		} `xml:"CancelarNfseResponse"`
	} `xml:"Body"`
}

// parseGerarNfseResponse parseia a resposta SOAP do GerarNfse.
// Em caso de erro retornado pela prefeitura, agrega as mensagens em um único error.
func parseGerarNfseResponse(soapBody string) (*NFSeResponse, error) {
	// Verificar SOAP Fault primeiro
	var fault soapFaultResponse
	if err := xml.Unmarshal([]byte(soapBody), &fault); err == nil && fault.Body.Fault.FaultCode != "" {
		return nil, fmt.Errorf("SOAP Fault [%s]: %s", fault.Body.Fault.FaultCode, fault.Body.Fault.FaultString)
	}

	var resp gerarNfseSOAPResponse
	if err := xml.Unmarshal([]byte(soapBody), &resp); err != nil {
		return nil, fmt.Errorf("erro ao parsear resposta SOAP GerarNfse: %w", err)
	}

	// Verificar erros retornados pelo webservice
	msgs := resp.Body.GerarNfseResponse.OutputXML.GerarNfseResposta.ListaMensagemRetorno.MensagemRetorno
	if len(msgs) > 0 {
		var errs []string
		for _, m := range msgs {
			errs = append(errs, fmt.Sprintf("[%s] %s — %s", m.Codigo, m.Mensagem, m.Correcao))
		}
		return nil, fmt.Errorf("webservice retornou erros: %s", strings.Join(errs, "; "))
	}

	inf := resp.Body.GerarNfseResponse.OutputXML.GerarNfseResposta.ListaNfse.CompNfse.Nfse.InfNfse
	if inf.Numero == "" {
		return nil, fmt.Errorf("resposta do webservice não contém número da NFS-e — verifique o XML de resposta")
	}

	return &NFSeResponse{
		Numero:            inf.Numero,
		CodigoVerificacao: inf.CodigoVerificacao,
		XML:               soapBody,
	}, nil
}

// parseCancelarNfseResponse parseia a resposta SOAP do CancelarNfse.
func parseCancelarNfseResponse(soapBody string) error {
	var fault soapFaultResponse
	if err := xml.Unmarshal([]byte(soapBody), &fault); err == nil && fault.Body.Fault.FaultCode != "" {
		return fmt.Errorf("SOAP Fault [%s]: %s", fault.Body.Fault.FaultCode, fault.Body.Fault.FaultString)
	}

	var resp cancelarNfseSOAPResponse
	if err := xml.Unmarshal([]byte(soapBody), &resp); err != nil {
		return fmt.Errorf("erro ao parsear resposta SOAP CancelarNfse: %w", err)
	}

	msgs := resp.Body.CancelarNfseResponse.OutputXML.CancelarNfseResposta.ListaMensagemRetorno.MensagemRetorno
	if len(msgs) > 0 {
		var errs []string
		for _, m := range msgs {
			errs = append(errs, fmt.Sprintf("[%s] %s", m.Codigo, m.Mensagem))
		}
		return fmt.Errorf("webservice retornou erros no cancelamento: %s", strings.Join(errs, "; "))
	}

	return nil
}

// --- Stubs para homologação sem certificado ---

func (c *NFSeClient) stubEnviarRPS(rps *RPS) (*NFSeResponse, error) {
	// Não logar dados do tomador (R5)
	slog.Info("nfse-stub EnviarRPS",
		slog.String("provider_cnpj", c.providerCNPJ),
		slog.String("serie", rps.Serie),
		slog.String("numero", rps.Numero),
		slog.Float64("valor_servicos", rps.ValorServicos),
	)

	numero := fmt.Sprintf("STUB-%d", time.Now().UnixMilli())
	return &NFSeResponse{
		Numero:            numero,
		CodigoVerificacao: fmt.Sprintf("STUB-VER-%d", time.Now().UnixMilli()),
		XML:               fmt.Sprintf(`<CompNfse><Nfse><InfNfse><Numero>%s</Numero></InfNfse></Nfse></CompNfse>`, numero),
	}, nil
}

func (c *NFSeClient) stubCancelarNFSe(nfseNumber, motivo string) error {
	slog.Info("nfse-stub CancelarNFSe",
		slog.String("nfse_number", nfseNumber),
		slog.String("motivo", motivo),
	)
	return nil
}
