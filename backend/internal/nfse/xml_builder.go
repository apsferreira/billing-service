package nfse

import (
	"fmt"
	"strings"
	"time"
)

const (
	nsABRASF = "http://www.abrasf.org.br/nfse.xsd"

	// CodigoMunicipioSalvador é o código IBGE do município de Salvador/BA.
	CodigoMunicipioSalvador = 2927408
)

// BuildEnviarLoteRpsEnvio monta o XML do envelope de envio de lote de RPS
// conforme o schema ABRASF v2.04.
//
// O XML retornado ainda não está assinado — deve ser passado para SignXML antes
// de ser encapsulado no envelope SOAP.
func BuildEnviarLoteRpsEnvio(rps *RPS, loteID string) (string, error) {
	if rps == nil {
		return "", fmt.Errorf("rps não pode ser nil")
	}

	dataEmissao := rps.DataEmissao.Format("2006-01-02")
	dataEmissaoCompleta := rps.DataEmissao.Format(time.RFC3339)

	issRetido := "2" // 2 = não retido
	if rps.ISSRetido {
		issRetido = "1"
	}

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`<EnviarLoteRpsEnvio xmlns="%s">`, nsABRASF))
	sb.WriteString("\n")

	// LoteRps
	sb.WriteString("  <LoteRps Id=\"lote1\" versao=\"2.04\">\n")
	sb.WriteString(fmt.Sprintf("    <NumeroLote>%s</NumeroLote>\n", escapeXML(loteID)))
	sb.WriteString(fmt.Sprintf("    <CpfCnpj><Cnpj>%s</Cnpj></CpfCnpj>\n", escapeXML(cleanDoc(rps.Prestador.CNPJ))))
	sb.WriteString(fmt.Sprintf("    <InscricaoMunicipal>%s</InscricaoMunicipal>\n", escapeXML(cleanDoc(rps.Prestador.InscricaoMunicipal))))
	sb.WriteString("    <QuantidadeRps>1</QuantidadeRps>\n")
	sb.WriteString("    <ListaRps>\n")

	// InfRps
	sb.WriteString(fmt.Sprintf("      <Rps><InfDeclaracaoPrestacaoServico Id=\"rps%s\">\n", escapeXML(rps.Numero)))

	// Rps (identificação)
	rpsTypeCode := int(rps.Tipo)
	if rpsTypeCode == 0 {
		rpsTypeCode = int(RPSTypeRPS) // default: RPS normal
	}

	sb.WriteString("        <Rps>\n")
	sb.WriteString(fmt.Sprintf("          <IdentificacaoRps>\n"))
	sb.WriteString(fmt.Sprintf("            <Numero>%s</Numero>\n", escapeXML(rps.Numero)))
	sb.WriteString(fmt.Sprintf("            <Serie>%s</Serie>\n", escapeXML(rps.Serie)))
	sb.WriteString(fmt.Sprintf("            <Tipo>%d</Tipo>\n", rpsTypeCode))
	sb.WriteString("          </IdentificacaoRps>\n")
	sb.WriteString(fmt.Sprintf("          <DataEmissao>%s</DataEmissao>\n", dataEmissao))
	sb.WriteString("          <Status>1</Status>\n") // 1 = Normal
	sb.WriteString("        </Rps>\n")

	// Competência
	sb.WriteString(fmt.Sprintf("        <Competencia>%s</Competencia>\n", dataEmissaoCompleta))

	// Serviço
	sb.WriteString("        <Servico>\n")
	sb.WriteString("          <Valores>\n")
	sb.WriteString(fmt.Sprintf("            <ValorServicos>%.2f</ValorServicos>\n", rps.ValorServicos))
	if rps.ValorDeducoes > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorDeducoes>%.2f</ValorDeducoes>\n", rps.ValorDeducoes))
	}
	if rps.ValorPIS > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorPis>%.2f</ValorPis>\n", rps.ValorPIS))
	}
	if rps.ValorCOFINS > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorCofins>%.2f</ValorCofins>\n", rps.ValorCOFINS))
	}
	if rps.ValorINSS > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorInss>%.2f</ValorInss>\n", rps.ValorINSS))
	}
	if rps.ValorIR > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorIr>%.2f</ValorIr>\n", rps.ValorIR))
	}
	if rps.ValorCSLL > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorCsll>%.2f</ValorCsll>\n", rps.ValorCSLL))
	}
	sb.WriteString(fmt.Sprintf("            <IssRetido>%s</IssRetido>\n", issRetido))
	if rps.ValorISS > 0 {
		sb.WriteString(fmt.Sprintf("            <ValorIss>%.2f</ValorIss>\n", rps.ValorISS))
	}
	if rps.BaseCalculo > 0 {
		sb.WriteString(fmt.Sprintf("            <BaseCalculo>%.2f</BaseCalculo>\n", rps.BaseCalculo))
	}
	sb.WriteString(fmt.Sprintf("            <Aliquota>%.4f</Aliquota>\n", rps.Aliquota/100)) // ABRASF espera decimal (0.05 = 5%)
	sb.WriteString(fmt.Sprintf("            <ValorLiquidoNfse>%.2f</ValorLiquidoNfse>\n", rps.ValorLiquidoNfse))
	sb.WriteString("          </Valores>\n")
	sb.WriteString(fmt.Sprintf("          <ItemListaServico>%s</ItemListaServico>\n", escapeXML(rps.ItemListaServico)))
	sb.WriteString(fmt.Sprintf("          <Discriminacao>%s</Discriminacao>\n", escapeXML(rps.Discriminacao)))
	sb.WriteString(fmt.Sprintf("          <CodigoMunicipio>%d</CodigoMunicipio>\n", rps.CodigoMunicipio))
	sb.WriteString("          <ExigibilidadeISS>1</ExigibilidadeISS>\n") // 1 = Exigível
	sb.WriteString(fmt.Sprintf("          <MunicipioIncidencia>%d</MunicipioIncidencia>\n", CodigoMunicipioSalvador))
	sb.WriteString("        </Servico>\n")

	// Prestador
	sb.WriteString("        <Prestador>\n")
	sb.WriteString("          <CpfCnpj>\n")
	sb.WriteString(fmt.Sprintf("            <Cnpj>%s</Cnpj>\n", escapeXML(cleanDoc(rps.Prestador.CNPJ))))
	sb.WriteString("          </CpfCnpj>\n")
	if rps.Prestador.InscricaoMunicipal != "" {
		sb.WriteString(fmt.Sprintf("          <InscricaoMunicipal>%s</InscricaoMunicipal>\n", escapeXML(cleanDoc(rps.Prestador.InscricaoMunicipal))))
	}
	sb.WriteString("        </Prestador>\n")

	// Tomador
	if rps.Tomador.CNPJ != "" || rps.Tomador.CPF != "" || rps.Tomador.RazaoSocial != "" {
		sb.WriteString("        <TomadorServico>\n")

		if rps.Tomador.CNPJ != "" || rps.Tomador.CPF != "" {
			sb.WriteString("          <IdentificacaoTomador>\n")
			sb.WriteString("            <CpfCnpj>\n")
			if rps.Tomador.CNPJ != "" {
				sb.WriteString(fmt.Sprintf("              <Cnpj>%s</Cnpj>\n", escapeXML(cleanDoc(rps.Tomador.CNPJ))))
			} else {
				sb.WriteString(fmt.Sprintf("              <Cpf>%s</Cpf>\n", escapeXML(cleanDoc(rps.Tomador.CPF))))
			}
			sb.WriteString("            </CpfCnpj>\n")
			sb.WriteString("          </IdentificacaoTomador>\n")
		}

		if rps.Tomador.RazaoSocial != "" {
			sb.WriteString(fmt.Sprintf("          <RazaoSocial>%s</RazaoSocial>\n", escapeXML(rps.Tomador.RazaoSocial)))
		}

		e := rps.Tomador.Endereco
		if e.Logradouro != "" {
			sb.WriteString("          <Endereco>\n")
			sb.WriteString(fmt.Sprintf("            <Endereco>%s</Endereco>\n", escapeXML(e.Logradouro)))
			if e.Numero != "" {
				sb.WriteString(fmt.Sprintf("            <Numero>%s</Numero>\n", escapeXML(e.Numero)))
			}
			if e.Complemento != "" {
				sb.WriteString(fmt.Sprintf("            <Complemento>%s</Complemento>\n", escapeXML(e.Complemento)))
			}
			if e.Bairro != "" {
				sb.WriteString(fmt.Sprintf("            <Bairro>%s</Bairro>\n", escapeXML(e.Bairro)))
			}
			if e.CodigoMunicipio > 0 {
				sb.WriteString(fmt.Sprintf("            <CodigoMunicipio>%d</CodigoMunicipio>\n", e.CodigoMunicipio))
			}
			if e.Uf != "" {
				sb.WriteString(fmt.Sprintf("            <Uf>%s</Uf>\n", escapeXML(e.Uf)))
			}
			if e.CEP != "" {
				sb.WriteString(fmt.Sprintf("            <Cep>%s</Cep>\n", escapeXML(cleanDoc(e.CEP))))
			}
			sb.WriteString("          </Endereco>\n")
		}

		if rps.Tomador.Email != "" {
			sb.WriteString(fmt.Sprintf("          <Contato><Email>%s</Email></Contato>\n", escapeXML(rps.Tomador.Email)))
		}

		sb.WriteString("        </TomadorServico>\n")
	}

	// NaturezaOperacao
	sb.WriteString(fmt.Sprintf("        <NaturezaOperacao>%d</NaturezaOperacao>\n", rps.NaturezaOperacao))

	sb.WriteString("      </InfDeclaracaoPrestacaoServico></Rps>\n")
	sb.WriteString("    </ListaRps>\n")
	sb.WriteString("  </LoteRps>\n")
	sb.WriteString("</EnviarLoteRpsEnvio>")

	return sb.String(), nil
}

// BuildCancelarNfseEnvio monta o XML de solicitação de cancelamento de NFS-e
// conforme schema ABRASF v2.04.
func BuildCancelarNfseEnvio(providerCNPJ, providerIM, nfseNumero, codigoMunicipio string, motivo int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`<CancelarNfseEnvio xmlns="%s">`, nsABRASF))
	sb.WriteString("\n")
	sb.WriteString("  <Pedido>\n")
	sb.WriteString("    <InfPedidoCancelamento Id=\"cancel1\">\n")
	sb.WriteString("      <IdentificacaoNfse>\n")
	sb.WriteString(fmt.Sprintf("        <Numero>%s</Numero>\n", escapeXML(nfseNumero)))
	sb.WriteString("        <CpfCnpj>\n")
	sb.WriteString(fmt.Sprintf("          <Cnpj>%s</Cnpj>\n", escapeXML(cleanDoc(providerCNPJ))))
	sb.WriteString("        </CpfCnpj>\n")
	if providerIM != "" {
		sb.WriteString(fmt.Sprintf("        <InscricaoMunicipal>%s</InscricaoMunicipal>\n", escapeXML(cleanDoc(providerIM))))
	}
	sb.WriteString(fmt.Sprintf("        <CodigoMunicipio>%s</CodigoMunicipio>\n", escapeXML(codigoMunicipio)))
	sb.WriteString("      </IdentificacaoNfse>\n")
	sb.WriteString(fmt.Sprintf("      <CodigoCancelamento>%d</CodigoCancelamento>\n", motivo))
	sb.WriteString("    </InfPedidoCancelamento>\n")
	sb.WriteString("  </Pedido>\n")
	sb.WriteString("</CancelarNfseEnvio>")

	return sb.String()
}

// BuildSOAPEnvelope encapsula um body XML no envelope SOAP 1.1 com o SOAPAction correto.
func BuildSOAPEnvelope(bodyContent string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
                  xmlns:nfse="%s">
  <soapenv:Header/>
  <soapenv:Body>
    %s
  </soapenv:Body>
</soapenv:Envelope>`, nsABRASF, bodyContent)
}

// escapeXML escapa os caracteres especiais de XML em um valor de texto.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// cleanDoc remove caracteres não-numéricos de documentos (CPF, CNPJ, CEP).
func cleanDoc(doc string) string {
	var sb strings.Builder
	for _, r := range doc {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
