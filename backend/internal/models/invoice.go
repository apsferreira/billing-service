package models

import (
	"time"

	"github.com/google/uuid"
)

// InvoiceStatus representa os estados possíveis de uma NFS-e.
type InvoiceStatus string

const (
	StatusPending    InvoiceStatus = "pending"
	StatusProcessing InvoiceStatus = "processing"
	StatusIssued     InvoiceStatus = "issued"
	StatusFailed     InvoiceStatus = "failed"
	StatusCancelled  InvoiceStatus = "cancelled"
)

// RPSType define o tipo de RPS conforme ABRASF.
type RPSType string

const (
	RPSTypeNormal   RPSType = "RPS"
	RPSTypeDevolucao RPSType = "RPS-D"
)

// Invoice representa uma fatura e sua NFS-e associada.
type Invoice struct {
	ID                 uuid.UUID     `json:"id"`
	OrderID            uuid.UUID     `json:"order_id"`
	CustomerID         uuid.UUID     `json:"customer_id"`
	TenantID           *uuid.UUID    `json:"tenant_id,omitempty"`
	Amount             float64       `json:"amount"`
	ServiceDescription string        `json:"service_description"`
	Status             InvoiceStatus `json:"status"`
	RPSType            RPSType       `json:"rps_type"`
	CDCDeadline        *time.Time    `json:"cdc_deadline,omitempty"`
	ReversedByInvoiceID *uuid.UUID   `json:"reversed_by_invoice_id,omitempty"`
	OriginalInvoiceID  *uuid.UUID    `json:"original_invoice_id,omitempty"`
	NfseNumber         *string       `json:"nfse_number,omitempty"`
	NfseCode           *string       `json:"nfse_code,omitempty"`
	NfseXML            *string       `json:"nfse_xml,omitempty"`
	NfsePDFURL         *string       `json:"nfse_pdf_url,omitempty"`
	ErrorMessage       *string       `json:"error_message,omitempty"`
	Attempts           int           `json:"attempts"`
	IssuedAt           *time.Time    `json:"issued_at,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

// PaymentConfirmedEvent representa o evento recebido via RabbitMQ.
// Compatível com o envelope IITPaymentConfirmedData do checkout-service.
type PaymentConfirmedEvent struct {
	OrderID            string  `json:"order_id"`
	CustomerID         string  `json:"customer_id"`
	TenantID           string  `json:"tenant_id,omitempty"`
	Amount             float64 `json:"amount"`
	Currency           string  `json:"currency,omitempty"`
	PaymentMethod      string  `json:"payment_method,omitempty"`
	AsaasPaymentID     string  `json:"asaas_payment_id,omitempty"`
	ServiceDescription string  `json:"service_description,omitempty"`
}

// SubscriptionCancelledEvent representa o evento recebido quando uma assinatura é cancelada.
type SubscriptionCancelledEvent struct {
	SubscriptionID string `json:"subscription_id"`
	CustomerID     string `json:"customer_id"`
	OrderID        string `json:"order_id"`
	// Reason: "cdc_art49" para cancelamento dentro do prazo CDC Art. 49,
	// ou "customer_request" para cancelamento comum.
	Reason string `json:"reason"`
}

// ListInvoicesFilter filtros disponíveis para listagem de faturas.
type ListInvoicesFilter struct {
	Status     InvoiceStatus `query:"status"`
	CustomerID string        `query:"customer_id"`
	Page       int           `query:"page"`
	PageSize   int           `query:"page_size"`
}

// PaginatedInvoices resposta paginada de faturas.
type PaginatedInvoices struct {
	Data     []Invoice `json:"data"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}
