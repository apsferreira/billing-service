package nfse

import "time"

// RPSTypeCode define o código numérico do tipo de RPS conforme ABRASF v2.04.
// 1 = RPS (nota normal), 3 = RPS-D (nota de devolução/estorno).
type RPSTypeCode int

const (
	RPSTypeRPS  RPSTypeCode = 1 // Nota de serviço normal
	RPSTypeRPSD RPSTypeCode = 3 // Nota de devolução (estorno CDC / RPS-D)
)

// RPS — Recibo Provisório de Serviços.
// Documento enviado ao webservice da prefeitura para solicitar emissão de NFS-e.
// Estrutura conforme Padrão ABRASF v2.04 (Salvador/BA).
type RPS struct {
	// Identificação do RPS
	// Tipo: 1 = RPS, 3 = RPS-D (devolução). Zero value trata-se como RPS.
	Tipo        RPSTypeCode `xml:"Tipo"`
	Serie       string      `xml:"Serie"`
	Numero      string      `xml:"Numero"`
	DataEmissao time.Time   `xml:"DataEmissao"`

	// Natureza da operação (1 = tributação no município)
	NaturezaOperacao int `xml:"NaturezaOperacao"`

	// Dados do serviço
	ValorServicos    float64 `xml:"ValorServicos"`
	ValorDeducoes    float64 `xml:"ValorDeducoes"`
	ValorPIS         float64 `xml:"ValorPis"`
	ValorCOFINS      float64 `xml:"ValorCofins"`
	ValorINSS        float64 `xml:"ValorInss"`
	ValorIR          float64 `xml:"ValorIr"`
	ValorCSLL        float64 `xml:"ValorCsll"`
	ISSRetido        bool    `xml:"IssRetido"`
	ValorISS         float64 `xml:"ValorIss"`
	BaseCalculo      float64 `xml:"BaseCalculo"`
	Aliquota         float64 `xml:"Aliquota"`
	ValorLiquidoNfse float64 `xml:"ValorLiquidoNfse"`

	// Código do serviço na lista ABRASF (ex: "01.07")
	ItemListaServico string `xml:"ItemListaServico"`

	// Descrição do serviço prestado
	Discriminacao string `xml:"Discriminacao"`

	// Código do município do serviço (Salvador: 2927408)
	CodigoMunicipio int `xml:"CodigoMunicipio"`

	// Dados do prestador
	Prestador PessoaJuridica `xml:"Prestador"`

	// Dados do tomador
	Tomador Tomador `xml:"Tomador"`
}

// PessoaJuridica representa o prestador de serviços.
type PessoaJuridica struct {
	CNPJ              string `xml:"Cnpj"`
	InscricaoMunicipal string `xml:"InscricaoMunicipal"`
}

// Tomador representa o tomador dos serviços (cliente).
type Tomador struct {
	// CPF ou CNPJ (apenas um deve ser preenchido)
	CPF  string `xml:"Cpf,omitempty"`
	CNPJ string `xml:"Cnpj,omitempty"`

	RazaoSocial string  `xml:"RazaoSocial"`
	Endereco    Endereco `xml:"Endereco,omitempty"`
	Email       string  `xml:"Email,omitempty"`
}

// Endereco representa um endereço no padrão ABRASF.
type Endereco struct {
	Logradouro       string `xml:"Endereco,omitempty"`
	Numero           string `xml:"Numero,omitempty"`
	Complemento      string `xml:"Complemento,omitempty"`
	Bairro           string `xml:"Bairro,omitempty"`
	CodigoMunicipio  int    `xml:"CodigoMunicipio,omitempty"`
	Uf               string `xml:"Uf,omitempty"`
	CEP              string `xml:"Cep,omitempty"`
}

// NFSeResponse é o retorno do webservice após emissão bem-sucedida.
type NFSeResponse struct {
	Numero            string `json:"numero"`
	CodigoVerificacao string `json:"codigo_verificacao"`
	XML               string `json:"xml"`
}
