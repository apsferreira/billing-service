package service

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	"billing-service/internal/models"
	"billing-service/internal/nfse"
	"billing-service/internal/repository"
)

// BillingService orquestra criação e emissão de NFS-e a partir de eventos de pagamento.
type BillingService struct {
	repo       *repository.InvoiceRepository
	nfseClient *nfse.NFSeClient
	aliquota   float64
	itemLista  string
}

// NewBillingService cria um novo serviço de faturamento.
func NewBillingService(repo *repository.InvoiceRepository, nfseClient *nfse.NFSeClient, aliquota float64, itemLista string) *BillingService {
	return &BillingService{
		repo:       repo,
		nfseClient: nfseClient,
		aliquota:   aliquota,
		itemLista:  itemLista,
	}
}

// CreateFromPaymentEvent cria uma fatura a partir de um evento payment.confirmed.
// Idempotente: se já existir fatura para o order_id, retorna a existente.
func (s *BillingService) CreateFromPaymentEvent(ctx context.Context, event models.PaymentConfirmedEvent) (*models.Invoice, error) {
	orderID, err := uuid.Parse(event.OrderID)
	if err != nil {
		return nil, err
	}

	// Idempotência: checar se já existe fatura para este pedido
	existing, err := s.repo.GetByOrderID(ctx, orderID)
	if err == nil && existing != nil {
		log.Printf("[billing] fatura já existe para order_id=%s id=%s status=%s", orderID, existing.ID, existing.Status)
		return existing, nil
	}

	customerID, err := uuid.Parse(event.CustomerID)
	if err != nil {
		return nil, err
	}

	desc := event.ServiceDescription
	if desc == "" {
		desc = "Serviços de tecnologia"
	}

	inv := &models.Invoice{
		ID:                 uuid.New(),
		OrderID:            orderID,
		CustomerID:         customerID,
		Amount:             event.Amount,
		ServiceDescription: desc,
		Status:             models.StatusPending,
		Attempts:           0,
	}

	if event.TenantID != "" {
		tid, err := uuid.Parse(event.TenantID)
		if err == nil {
			inv.TenantID = &tid
		}
	}

	if err := s.repo.Create(ctx, inv); err != nil {
		return nil, err
	}

	log.Printf("[billing] fatura criada id=%s order_id=%s amount=%.2f", inv.ID, inv.OrderID, inv.Amount)
	return inv, nil
}

// ProcessInvoice tenta emitir a NFS-e para uma fatura pendente ou com falha.
func (s *BillingService) ProcessInvoice(ctx context.Context, invoiceID uuid.UUID) error {
	inv, err := s.repo.GetByID(ctx, invoiceID)
	if err != nil {
		return err
	}

	if inv.Status == models.StatusIssued || inv.Status == models.StatusCancelled {
		log.Printf("[billing] fatura id=%s já está em status final %s — ignorando", inv.ID, inv.Status)
		return nil
	}

	// Marcar como processing
	if err := s.repo.UpdateStatus(ctx, inv.ID, models.StatusProcessing, map[string]interface{}{}); err != nil {
		return err
	}

	if err := s.repo.IncrementAttempts(ctx, inv.ID); err != nil {
		log.Printf("[billing] erro ao incrementar tentativas id=%s: %v", inv.ID, err)
	}

	// Montar RPS
	rps := s.buildRPS(inv)

	// Emitir via webservice
	resp, err := s.nfseClient.EnviarRPS(ctx, rps)
	if err != nil {
		errMsg := err.Error()
		log.Printf("[billing] falha ao emitir NFS-e id=%s: %v", inv.ID, err)
		// Sem logar dados sensíveis do cliente (R5)
		if updateErr := s.repo.UpdateStatus(ctx, inv.ID, models.StatusFailed, map[string]interface{}{
			"error_message": errMsg,
		}); updateErr != nil {
			log.Printf("[billing] erro ao persistir falha id=%s: %v", inv.ID, updateErr)
		}
		return err
	}

	// Sucesso: persistir dados da NFS-e emitida
	issuedAt := time.Now().UTC()
	if err := s.repo.UpdateStatus(ctx, inv.ID, models.StatusIssued, map[string]interface{}{
		"nfse_number": resp.Numero,
		"nfse_code":   resp.CodigoVerificacao,
		"nfse_xml":    resp.XML,
		"issued_at":   issuedAt,
		"error_message": nil,
	}); err != nil {
		return err
	}

	log.Printf("[billing] NFS-e emitida id=%s nfse_number=%s", inv.ID, resp.Numero)
	return nil
}

// RetryInvoice reprocessa uma fatura que falhou.
func (s *BillingService) RetryInvoice(ctx context.Context, invoiceID uuid.UUID) error {
	inv, err := s.repo.GetByID(ctx, invoiceID)
	if err != nil {
		return err
	}

	if inv.Status != models.StatusFailed {
		return &ErrInvalidTransition{Current: string(inv.Status), Target: string(models.StatusPending)}
	}

	// Reset para pending antes de reprocessar
	if err := s.repo.UpdateStatus(ctx, inv.ID, models.StatusPending, map[string]interface{}{}); err != nil {
		return err
	}

	return s.ProcessInvoice(ctx, invoiceID)
}

// GetInvoice retorna uma fatura pelo ID.
func (s *BillingService) GetInvoice(ctx context.Context, id uuid.UUID) (*models.Invoice, error) {
	return s.repo.GetByID(ctx, id)
}

// ListInvoices retorna faturas paginadas com filtros.
func (s *BillingService) ListInvoices(ctx context.Context, filter models.ListInvoicesFilter) (*models.PaginatedInvoices, error) {
	invoices, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &models.PaginatedInvoices{
		Data:     invoices,
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}

// buildRPS monta o RPS a partir dos dados da fatura.
// O tomador será preenchido com dados mínimos — numa integração real
// os dados do cliente viriam do customer-service.
func (s *BillingService) buildRPS(inv *models.Invoice) *nfse.RPS {
	aliquotaDecimal := s.aliquota / 100
	iss := inv.Amount * aliquotaDecimal

	return &nfse.RPS{
		Serie:            "A",
		Numero:           inv.ID.String(),
		DataEmissao:      time.Now().UTC(),
		NaturezaOperacao: 1, // tributação no município do prestador
		ValorServicos:    inv.Amount,
		ValorDeducoes:    0,
		ISSRetido:        false,
		ValorISS:         iss,
		BaseCalculo:      inv.Amount,
		Aliquota:         aliquotaDecimal,
		ValorLiquidoNfse: inv.Amount - iss,
		ItemListaServico: s.itemLista,
		Discriminacao:    inv.ServiceDescription,
		CodigoMunicipio:  2927408, // IBGE Salvador/BA
		Tomador: nfse.Tomador{
			RazaoSocial: "Cliente " + inv.CustomerID.String(),
		},
	}
}

// ErrInvalidTransition indica tentativa de transição de status inválida.
type ErrInvalidTransition struct {
	Current string
	Target  string
}

func (e *ErrInvalidTransition) Error() string {
	return "transição de status inválida: " + e.Current + " -> " + e.Target
}
