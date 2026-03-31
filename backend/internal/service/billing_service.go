package service

import (
	"context"
	"fmt"
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
// Se for a primeira fatura do cliente, define o prazo CDC Art. 49 (7 dias).
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
		RPSType:            models.RPSTypeNormal,
		Attempts:           0,
	}

	if event.TenantID != "" {
		tid, err := uuid.Parse(event.TenantID)
		if err == nil {
			inv.TenantID = &tid
		}
	}

	// CDC Art. 49: definir prazo de 7 dias apenas na primeira fatura do cliente
	count, err := s.repo.CountIssuedByCustomer(ctx, customerID)
	if err != nil {
		log.Printf("[billing] erro ao verificar histórico do cliente id=%s: %v", customerID, err)
		// Não bloquear criação — tratar como não-primeira para segurança
	} else if count == 0 {
		deadline := time.Now().UTC().AddDate(0, 0, 7)
		inv.CDCDeadline = &deadline
		log.Printf("[billing] prazo CDC definido para fatura id=%s deadline=%s", inv.ID, deadline.Format(time.RFC3339))
	}

	if err := s.repo.Create(ctx, inv); err != nil {
		return nil, err
	}

	log.Printf("[billing] fatura criada id=%s order_id=%s amount=%.2f rps_type=%s",
		inv.ID, inv.OrderID, inv.Amount, inv.RPSType)
	return inv, nil
}

// CreateReversal cria uma fatura de estorno (RPS-D) referenciando a nota original.
// Persiste o vínculo bidirecional entre original e estorno.
func (s *BillingService) CreateReversal(ctx context.Context, originalInvoiceID uuid.UUID, reason string) (*models.Invoice, error) {
	original, err := s.repo.GetByID(ctx, originalInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("fatura original não encontrada: %w", err)
	}

	// Impedir estorno de uma nota que já foi estornada
	if original.ReversedByInvoiceID != nil {
		return nil, &ErrAlreadyReversed{InvoiceID: originalInvoiceID}
	}

	reversal := &models.Invoice{
		ID:                 uuid.New(),
		OrderID:            original.OrderID,
		CustomerID:         original.CustomerID,
		TenantID:           original.TenantID,
		Amount:             original.Amount,
		ServiceDescription: "Estorno - " + original.ServiceDescription,
		Status:             models.StatusPending,
		RPSType:            models.RPSTypeDevolucao,
		OriginalInvoiceID:  &original.ID,
	}

	if err := s.repo.Create(ctx, reversal); err != nil {
		return nil, fmt.Errorf("erro ao criar fatura de estorno: %w", err)
	}

	// Atualizar nota original com referência ao estorno
	if err := s.repo.UpdateStatus(ctx, original.ID, original.Status, map[string]interface{}{
		"reversed_by_invoice_id": reversal.ID,
	}); err != nil {
		log.Printf("[billing] aviso: erro ao vincular estorno id=%s à original id=%s: %v",
			reversal.ID, original.ID, err)
	}

	log.Printf("[billing] estorno criado id=%s original_id=%s reason=%s", reversal.ID, original.ID, reason)
	return reversal, nil
}

// ProcessInvoice tenta emitir a NFS-e para uma fatura pendente ou com falha.
// Funciona tanto para RPS (nota normal) quanto para RPS-D (estorno).
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

	// Montar RPS (normal ou devolução)
	rps := s.buildRPS(inv)

	// Emitir via webservice
	resp, err := s.nfseClient.EnviarRPS(ctx, rps)
	if err != nil {
		errMsg := err.Error()
		log.Printf("[billing] falha ao emitir NFS-e id=%s rps_type=%s: %v", inv.ID, inv.RPSType, err)
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
		"nfse_number":   resp.Numero,
		"nfse_code":     resp.CodigoVerificacao,
		"nfse_xml":      resp.XML,
		"issued_at":     issuedAt,
		"error_message": nil,
	}); err != nil {
		return err
	}

	log.Printf("[billing] NFS-e emitida id=%s rps_type=%s nfse_number=%s", inv.ID, inv.RPSType, resp.Numero)
	return nil
}

// HandleSubscriptionCancelled processa o evento subscription.cancelled.
// Se o motivo for "cdc_art49" e o prazo CDC ainda estiver vigente,
// cria uma nota de devolução (RPS-D) e a emite em background.
// Caso o prazo já tenha expirado, apenas marca a fatura como cancelada.
func (s *BillingService) HandleSubscriptionCancelled(ctx context.Context, event models.SubscriptionCancelledEvent) error {
	if event.OrderID == "" {
		return fmt.Errorf("order_id ausente no evento subscription.cancelled")
	}

	orderID, err := uuid.Parse(event.OrderID)
	if err != nil {
		return fmt.Errorf("order_id inválido: %w", err)
	}

	original, err := s.repo.GetByOrderID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("fatura não encontrada para order_id=%s: %w", event.OrderID, err)
	}

	if event.Reason == "cdc_art49" {
		if original.CDCDeadline != nil && time.Now().UTC().Before(*original.CDCDeadline) {
			// Dentro do prazo CDC: criar RPS-D e emitir
			reversal, err := s.CreateReversal(ctx, original.ID, event.Reason)
			if err != nil {
				return fmt.Errorf("erro ao criar estorno CDC: %w", err)
			}

			// Emissão do RPS-D em goroutine separada para não bloquear o consumer
			go func() {
				emitCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := s.ProcessInvoice(emitCtx, reversal.ID); err != nil {
					log.Printf("[billing] erro ao emitir RPS-D id=%s: %v", reversal.ID, err)
				}
			}()

			log.Printf("[billing] cancelamento CDC processado: estorno id=%s criado para fatura id=%s",
				reversal.ID, original.ID)
			return nil
		}

		// Prazo CDC expirado — apenas cancelar sem estorno fiscal
		log.Printf("[billing] prazo CDC expirado para fatura id=%s — cancelando sem RPS-D", original.ID)
	}

	// Cancelamento comum ou prazo CDC expirado
	if err := s.repo.UpdateStatus(ctx, original.ID, models.StatusCancelled, map[string]interface{}{}); err != nil {
		return fmt.Errorf("erro ao cancelar fatura id=%s: %w", original.ID, err)
	}

	log.Printf("[billing] fatura cancelada id=%s reason=%s", original.ID, event.Reason)
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
// O tipo RPS (normal ou devolução) é respeitado conforme inv.RPSType.
// O tomador será preenchido com dados mínimos — numa integração real
// os dados do cliente viriam do customer-service.
func (s *BillingService) buildRPS(inv *models.Invoice) *nfse.RPS {
	aliquotaDecimal := s.aliquota / 100
	iss := inv.Amount * aliquotaDecimal

	rpsType := nfse.RPSTypeRPS
	if inv.RPSType == models.RPSTypeDevolucao {
		rpsType = nfse.RPSTypeRPSD
	}

	return &nfse.RPS{
		Tipo:             rpsType,
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

// ErrAlreadyReversed indica que a fatura já possui um estorno associado.
type ErrAlreadyReversed struct {
	InvoiceID uuid.UUID
}

func (e *ErrAlreadyReversed) Error() string {
	return fmt.Sprintf("fatura %s já possui estorno — não é possível criar novo", e.InvoiceID)
}
