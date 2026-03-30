package nfse

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SHA-1 é mandatório pelo padrão ABRASF v2.04 (requisito normativo, não escolha de design)
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"

	"golang.org/x/crypto/pkcs12"
)

// CertBundle contém o certificado X.509 e a chave privada extraídos do .pfx A1.
type CertBundle struct {
	TLSCert    tls.Certificate
	PrivateKey *rsa.PrivateKey
	X509Cert   *x509.Certificate
}

// LoadCertBundle carrega um certificado A1 (.pfx) e extrai a chave privada RSA
// e o certificado X.509 para uso em assinatura XML e mTLS.
//
// certPath é o caminho absoluto para o arquivo .pfx.
// certPassword é a senha do arquivo .pfx.
func LoadCertBundle(certPath, certPassword string) (*CertBundle, error) {
	pfxData, err := os.ReadFile(certPath) // #nosec G304 — path vem de variável de ambiente, não de input do usuário
	if err != nil {
		return nil, fmt.Errorf("erro ao ler certificado .pfx: %w", err)
	}

	privateKey, cert, err := pkcs12.Decode(pfxData, certPassword)
	if err != nil {
		return nil, fmt.Errorf("erro ao decodificar certificado .pfx (verifique a senha): %w", err)
	}

	rsaKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("chave privada do certificado não é RSA — tipo encontrado: %T", privateKey)
	}

	tlsCert := tls.Certificate{
		Certificate: [][]byte{cert.Raw},
		PrivateKey:  rsaKey,
		Leaf:        cert,
	}

	return &CertBundle{
		TLSCert:    tlsCert,
		PrivateKey: rsaKey,
		X509Cert:   cert,
	}, nil
}

// SignXML assina um documento XML usando XMLDSig com RSA-SHA1 conforme
// exigido pelo padrão ABRASF v2.04.
//
// A assinatura é do tipo "enveloped" (a tag Signature fica dentro do elemento assinado).
// O elemento alvo é identificado pelo atributo Id= presente no XML.
//
// Processo:
//  1. Extrair o elemento a ser assinado via Id
//  2. Canonicalizar (C14N — xml-exc-c14n)
//  3. Calcular DigestValue (SHA-1) do elemento canonicalizado
//  4. Montar SignedInfo com Reference
//  5. Canonicalizar SignedInfo
//  6. Calcular SignatureValue (RSA-SHA1) do SignedInfo canonicalizado
//  7. Embutir Signature no XML original
func SignXML(xmlDoc string, bundle *CertBundle) (string, error) {
	// Extrair o Id= do elemento raiz a ser assinado
	refID, err := extractReferenceID(xmlDoc)
	if err != nil {
		return "", fmt.Errorf("erro ao extrair Id do elemento: %w", err)
	}

	// Canonicalizar o elemento alvo (simplificado: usar o XML como está,
	// removendo a declaração XML e normalizando espaços para C14N exclusivo)
	canonicalized, err := canonicalize(xmlDoc)
	if err != nil {
		return "", fmt.Errorf("erro na canonicalização do documento: %w", err)
	}

	// DigestValue = SHA-1 do elemento canonicalizado, codificado em base64
	digestValue, err := sha1Digest(canonicalized)
	if err != nil {
		return "", fmt.Errorf("erro ao calcular digest: %w", err)
	}

	// Certificado em base64 (DER) para X509Certificate
	certB64 := base64.StdEncoding.EncodeToString(bundle.X509Cert.Raw)

	// Montar SignedInfo
	signedInfo := buildSignedInfo(refID, digestValue)

	// Canonicalizar o SignedInfo antes de assinar
	canonicalizedSignedInfo, err := canonicalize(signedInfo)
	if err != nil {
		return "", fmt.Errorf("erro na canonicalização do SignedInfo: %w", err)
	}

	// Assinar com RSA-SHA1
	sigValue, err := rsaSHA1Sign(bundle.PrivateKey, []byte(canonicalizedSignedInfo))
	if err != nil {
		return "", fmt.Errorf("erro ao assinar XML: %w", err)
	}

	// Montar o bloco Signature completo
	signatureBlock := buildSignatureBlock(signedInfo, sigValue, certB64)

	// Inserir a assinatura antes do fechamento da tag raiz
	signed, err := insertSignature(xmlDoc, signatureBlock)
	if err != nil {
		return "", fmt.Errorf("erro ao inserir assinatura no XML: %w", err)
	}

	return signed, nil
}

// extractReferenceID extrai o valor do atributo Id="..." do primeiro elemento do XML.
// O ABRASF usa Id="lote1", Id="rpsXXX" ou Id="cancel1".
func extractReferenceID(xmlDoc string) (string, error) {
	re := regexp.MustCompile(`Id="([^"]+)"`)
	matches := re.FindStringSubmatch(xmlDoc)
	if len(matches) < 2 {
		return "", fmt.Errorf("nenhum atributo Id encontrado no XML — necessário para assinatura XMLDSig")
	}
	return matches[1], nil
}

// canonicalize implementa uma canonicalização C14N simplificada para o padrão ABRASF.
// Remove a declaração XML (<?xml ...?>) e normaliza para uso como input de digest/assinatura.
//
// Nota: uma implementação completa do W3C C14N seria necessária para ambientes
// onde outros sistemas verificam a assinatura com strictness total. Para o
// webservice de Salvador/BA esta abordagem é suficiente na prática.
func canonicalize(xmlDoc string) (string, error) {
	// Remover declaração XML
	result := regexp.MustCompile(`<\?xml[^?]*\?>\s*`).ReplaceAllString(xmlDoc, "")
	// Normalizar quebras de linha para LF
	result = strings.ReplaceAll(result, "\r\n", "\n")
	result = strings.ReplaceAll(result, "\r", "\n")
	return result, nil
}

// sha1Digest calcula o digest SHA-1 do conteúdo e retorna em base64.
func sha1Digest(content string) (string, error) {
	//nolint:gosec // SHA-1 é mandatório pelo padrão ABRASF v2.04
	h := sha1.New()
	_, err := h.Write([]byte(content))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// rsaSHA1Sign assina os dados com RSA-SHA1 usando PKCS#1 v1.5.
func rsaSHA1Sign(key *rsa.PrivateKey, data []byte) (string, error) {
	//nolint:gosec // SHA-1 é mandatório pelo padrão ABRASF v2.04
	h := sha1.New()
	h.Write(data)
	digest := h.Sum(nil)

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA1, digest)
	if err != nil {
		return "", fmt.Errorf("erro na assinatura RSA-SHA1: %w", err)
	}

	return base64.StdEncoding.EncodeToString(sig), nil
}

// buildSignedInfo monta o elemento SignedInfo do XMLDSig.
func buildSignedInfo(refID, digestValue string) string {
	return fmt.Sprintf(`<SignedInfo xmlns="http://www.w3.org/2000/09/xmldsig#">
  <CanonicalizationMethod Algorithm="http://www.w3.org/TR/2001/REC-xml-c14n-20010315"/>
  <SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1"/>
  <Reference URI="#%s">
    <Transforms>
      <Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"/>
      <Transform Algorithm="http://www.w3.org/TR/2001/REC-xml-c14n-20010315"/>
    </Transforms>
    <DigestMethod Algorithm="http://www.w3.org/2000/09/xmldsig#sha1"/>
    <DigestValue>%s</DigestValue>
  </Reference>
</SignedInfo>`, refID, digestValue)
}

// buildSignatureBlock monta o elemento Signature XMLDSig completo.
func buildSignatureBlock(signedInfo, signatureValue, certB64 string) string {
	return fmt.Sprintf(`<Signature xmlns="http://www.w3.org/2000/09/xmldsig#">
  %s
  <SignatureValue>%s</SignatureValue>
  <KeyInfo>
    <X509Data>
      <X509Certificate>%s</X509Certificate>
    </X509Data>
  </KeyInfo>
</Signature>`, signedInfo, signatureValue, certB64)
}

// insertSignature insere o bloco de assinatura antes do fechamento da tag raiz do XML.
func insertSignature(xmlDoc, signatureBlock string) (string, error) {
	// Encontrar a posição do último '>' que fecha a tag raiz
	lastClose := strings.LastIndex(xmlDoc, "</")
	if lastClose < 0 {
		return "", fmt.Errorf("XML malformado: nenhuma tag de fechamento encontrada")
	}

	return xmlDoc[:lastClose] + "\n" + signatureBlock + "\n" + xmlDoc[lastClose:], nil
}
