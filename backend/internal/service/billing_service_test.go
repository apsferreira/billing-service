package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"billing-service/internal/models"
	"billing-service/internal/nfse"
)

// ── Mocks ─────────────────────────────────────────────────────────────────────

type mockInvoiceRepo struct {
	invoices     map[uuid.UUID]*models.Invoice
	byOrderID    map[uuid.UUID]*models.Invoice
	customerCount map[uuid.UUID]int
	createErr    error
	getErr       error
	updateErr    error
}

func newMockInvoiceRepo() *mockInvoiceRepo {
	return &mockInvoiceRepo{
		invoices:      make(map[uuid.UUID]*models.Invoice),
		byOrderID:     make(map[uuid.UUID]*models.Invoice),
		customerCount: make(map[uuid.UUID]int),
	}
}

func (m *mockInvoiceRepo) Create(ctx context.Context, invoice *models.Invoice) error {
	if m.createErr != nil {
		return m.createErr
	}
	invoice.CreatedAt = time.Now().UTC()
	invoice.UpdatedAt = time.Now().UTC()
	m.invoices[invoice.ID] = invoice
	m.byOrderID[invoice.OrderID] = invoice
	return nil
}

func (m *mockInvoiceRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Invoice, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	inv, ok := m.invoices[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return inv, nil
}

func (m *mockInvoiceRepo) GetByOrderID(ctx context.Context, orderID uuid.UUID) (*models.Invoice, error) {
	inv, ok := m.byOrderID[orderID]
	if !ok {
		return nil, nil
	}
	return inv, nil
}

func (m *mockInvoiceRepo) CountIssuedByCustomer(ctx context.Context, customerID uuid.UUID) (int, error) {
	return m.customerCount[customerID], nil
}

func (m *mockInvoiceRepo) List(ctx context.Context, filter models.ListInvoicesFilter) ([]models.Invoice, int, error) {
	var result []models.Invoice
	for _, inv := range m.invoices {
		result = append(result, *inv)
	}
	return result, len(result), nil
}

func (m *mockInvoiceRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status models.InvoiceStatus, fields map[string]interface{}) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	inv, ok := m.invoices[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	inv.Status = status
	return nil
}

func (m *mockInvoiceRepo) IncrementAttempts(ctx context.Context, id uuid.UUID) error {
	if inv, ok := m.invoices[id]; ok {
		inv.Attempts++
	}
	return nil
}

func (m *mockInvoiceRepo) CreateReversalAtomic(ctx context.Context, reversal *models.Invoice, originalID uuid.UUID) error {
	// Simula a operação atômica: cria o estorno e vincula à original
	m.invoices[reversal.ID] = reversal
	if original, ok := m.invoices[originalID]; ok {
		original.ReversedByInvoiceID = &reversal.ID
	}
	return nil
}

// mockNFSeClient simula o cliente NFS-e sem chamar a prefeitura.
type mockNFSeClient struct {
	shouldFail bool
	response   *nfse.NFSeResponse
}

func newMockNFSeClient(shouldFail bool) *mockNFSeClient {
	return &mockNFSeClient{
		shouldFail: shouldFail,
		response: &nfse.NFSeResponse{
			Numero:            "2026001",
			CodigoVerificacao: "ABC123",
			XML:               "<xml>nota</xml>",
		},
	}
}

func (m *mockNFSeClient) EnviarRPS(ctx context.Context, rps *nfse.RPS) (*nfse.NFSeResponse, error) {
	if m.shouldFail {
		return nil, fmt.Errorf("webservice indisponível")
	}
	return m.response, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newBillingSvc(nfseFails bool) (*BillingService, *mockInvoiceRepo) {
	repo := newMockInvoiceRepo()
	nfseClient := newMockNFSeClient(nfseFails)
	// Alíquota ISS Simples Nacional: 2% para Salvador/BA (Anexo III)
	svc := NewBillingService(repo, nfseClient, 2.0, "01.07")
	return svc, repo
}

func makePaymentEvent(amount float64) models.PaymentConfirmedEvent {
	return models.PaymentConfirmedEvent{
		OrderID:            uuid.New().String(),
		CustomerID:         uuid.New().String(),
		TenantID:           uuid.New().String(),
		Amount:             amount,
		ServiceDescription: "Serviço de tecnologia IIT",
	}
}

// ── CreateFromPaymentEvent ────────────────────────────────────────────────────

func TestBillingService_CreateFromPaymentEvent_Success(t *testing.T) {
	svc, repo := newBillingSvc(false)

	event := makePaymentEvent(99.90)

	inv, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if inv == nil {
		t.Fatal("esperava fatura não-nil")
	}
	if inv.Status != models.StatusPending {
		t.Errorf("status inicial deve ser pending, got %s", inv.Status)
	}
	if inv.Amount != 99.90 {
		t.Errorf("amount incorreto: got %.2f", inv.Amount)
	}
	if inv.RPSType != models.RPSTypeNormal {
		t.Errorf("RPSType deve ser RPS para nota normal, got %s", inv.RPSType)
	}
	if len(repo.invoices) != 1 {
		t.Errorf("esperava 1 fatura no repo, got %d", len(repo.invoices))
	}
}

func TestBillingService_CreateFromPaymentEvent_InvalidOrderID(t *testing.T) {
	svc, _ := newBillingSvc(false)

	event := models.PaymentConfirmedEvent{
		OrderID:    "uuid-invalido",
		CustomerID: uuid.New().String(),
		Amount:     99.0,
	}

	_, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err == nil {
		t.Fatal("esperava erro para order_id inválido")
	}
}

func TestBillingService_CreateFromPaymentEvent_InvalidCustomerID(t *testing.T) {
	svc, _ := newBillingSvc(false)

	event := models.PaymentConfirmedEvent{
		OrderID:    uuid.New().String(),
		CustomerID: "cpf-invalido",
		Amount:     99.0,
	}

	_, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err == nil {
		t.Fatal("esperava erro para customer_id inválido")
	}
}

func TestBillingService_CreateFromPaymentEvent_Idempotent(t *testing.T) {
	svc, repo := newBillingSvc(false)

	event := makePaymentEvent(199.0)

	// Primeira criação
	inv1, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("erro na primeira criação: %v", err)
	}

	// Segunda criação com mesmo order_id — deve retornar a existente
	inv2, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("erro na segunda criação: %v", err)
	}

	if inv1.ID != inv2.ID {
		t.Error("idempotência: esperava a mesma fatura na segunda chamada")
	}
	if len(repo.invoices) != 1 {
		t.Errorf("idempotência: esperava 1 fatura no repo, got %d", len(repo.invoices))
	}
}

func TestBillingService_CreateFromPaymentEvent_FirstCustomer_SetsCDCDeadline(t *testing.T) {
	svc, _ := newBillingSvc(false)

	event := makePaymentEvent(49.90)
	// customerCount = 0 no mock → primeira fatura → CDC Art. 49 deve ser aplicado

	inv, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if inv.CDCDeadline == nil {
		t.Error("primeira fatura deve ter CDC deadline definido (7 dias)")
	} else {
		// Deve ser aproximadamente 7 dias no futuro
		expectedDeadline := time.Now().AddDate(0, 0, 7)
		diff := inv.CDCDeadline.Sub(expectedDeadline)
		if diff < -time.Minute || diff > time.Minute {
			t.Errorf("CDC deadline incorreto: got %v, expected ~%v", inv.CDCDeadline, expectedDeadline)
		}
	}
}

func TestBillingService_CreateFromPaymentEvent_ExistingCustomer_NoCDCDeadline(t *testing.T) {
	svc, repo := newBillingSvc(false)

	customerID := uuid.New()
	// Simular que cliente já tem fatura emitida
	repo.customerCount[customerID] = 1

	event := models.PaymentConfirmedEvent{
		OrderID:    uuid.New().String(),
		CustomerID: customerID.String(),
		Amount:     99.0,
	}

	inv, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if inv.CDCDeadline != nil {
		t.Error("cliente existente não deve ter CDC deadline")
	}
}

func TestBillingService_CreateFromPaymentEvent_DefaultDescription(t *testing.T) {
	svc, _ := newBillingSvc(false)

	event := models.PaymentConfirmedEvent{
		OrderID:            uuid.New().String(),
		CustomerID:         uuid.New().String(),
		Amount:             49.0,
		ServiceDescription: "", // sem descrição → deve usar padrão
	}

	inv, err := svc.CreateFromPaymentEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if inv.ServiceDescription == "" {
		t.Error("descrição padrão deve ser aplicada quando ausente")
	}
}

// ── CreateReversal ────────────────────────────────────────────────────────────

func TestBillingService_CreateReversal_Success(t *testing.T) {
	svc, repo := newBillingSvc(false)

	// Criar fatura original primeiro
	originalID := uuid.New()
	orderID := uuid.New()
	original := &models.Invoice{
		ID:                 originalID,
		OrderID:            orderID,
		CustomerID:         uuid.New(),
		Amount:             149.90,
		ServiceDescription: "Plano anual",
		Status:             models.StatusIssued,
		RPSType:            models.RPSTypeNormal,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	repo.invoices[originalID] = original
	repo.byOrderID[orderID] = original

	reversal, err := svc.CreateReversal(context.Background(), originalID, "cdc_art49")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if reversal.RPSType != models.RPSTypeDevolucao {
		t.Errorf("estorno deve ter RPSType RPS-D, got %s", reversal.RPSType)
	}
	if reversal.OriginalInvoiceID == nil || *reversal.OriginalInvoiceID != originalID {
		t.Error("estorno deve referenciar fatura original")
	}
	if reversal.Amount != original.Amount {
		t.Errorf("estorno deve ter mesmo valor da original: got %.2f", reversal.Amount)
	}
}

func TestBillingService_CreateReversal_AlreadyReversed(t *testing.T) {
	svc, repo := newBillingSvc(false)

	reversalID := uuid.New()
	originalID := uuid.New()
	original := &models.Invoice{
		ID:                  originalID,
		OrderID:             uuid.New(),
		Amount:              99.0,
		Status:              models.StatusIssued,
		RPSType:             models.RPSTypeNormal,
		ReversedByInvoiceID: &reversalID, // já estornada
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	repo.invoices[originalID] = original

	_, err := svc.CreateReversal(context.Background(), originalID, "cdc_art49")
	if err == nil {
		t.Fatal("esperava erro para fatura já estornada")
	}
}

func TestBillingService_CreateReversal_NotFound(t *testing.T) {
	svc, _ := newBillingSvc(false)

	_, err := svc.CreateReversal(context.Background(), uuid.New(), "cdc_art49")
	if err == nil {
		t.Fatal("esperava erro para fatura não encontrada")
	}
}

// ── HandleSubscriptionCancelled ───────────────────────────────────────────────

func TestBillingService_HandleSubscriptionCancelled_MissingOrderID(t *testing.T) {
	svc, _ := newBillingSvc(false)

	err := svc.HandleSubscriptionCancelled(context.Background(), models.SubscriptionCancelledEvent{
		OrderID: "",
	})
	if err == nil {
		t.Fatal("esperava erro para order_id ausente")
	}
}

func TestBillingService_HandleSubscriptionCancelled_InvalidOrderID(t *testing.T) {
	svc, _ := newBillingSvc(false)

	err := svc.HandleSubscriptionCancelled(context.Background(), models.SubscriptionCancelledEvent{
		OrderID: "order-invalido",
		Reason:  "customer_request",
	})
	if err == nil {
		t.Fatal("esperava erro para order_id inválido")
	}
}

func TestBillingService_HandleSubscriptionCancelled_CommonCancellation(t *testing.T) {
	svc, repo := newBillingSvc(false)

	orderID := uuid.New()
	invoiceID := uuid.New()
	inv := &models.Invoice{
		ID:      invoiceID,
		OrderID: orderID,
		Status:  models.StatusIssued,
		Amount:  99.0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	repo.invoices[invoiceID] = inv
	repo.byOrderID[orderID] = inv

	err := svc.HandleSubscriptionCancelled(context.Background(), models.SubscriptionCancelledEvent{
		OrderID: orderID.String(),
		Reason:  "customer_request",
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if repo.invoices[invoiceID].Status != models.StatusCancelled {
		t.Errorf("fatura deve estar cancelada, got %s", repo.invoices[invoiceID].Status)
	}
}

func TestBillingService_HandleSubscriptionCancelled_CDC_WithinDeadline(t *testing.T) {
	svc, repo := newBillingSvc(false)

	orderID := uuid.New()
	invoiceID := uuid.New()
	deadline := time.Now().UTC().AddDate(0, 0, 5) // ainda dentro do prazo
	inv := &models.Invoice{
		ID:          invoiceID,
		OrderID:     orderID,
		Status:      models.StatusIssued,
		Amount:      49.0,
		CDCDeadline: &deadline,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	repo.invoices[invoiceID] = inv
	repo.byOrderID[orderID] = inv

	err := svc.HandleSubscriptionCancelled(context.Background(), models.SubscriptionCancelledEvent{
		OrderID:    orderID.String(),
		CustomerID: uuid.New().String(),
		Reason:     "cdc_art49",
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	// Estorno criado — deve haver 2 faturas no repo (original + RPS-D)
	if len(repo.invoices) < 2 {
		t.Error("cancelamento CDC deve criar fatura de estorno (RPS-D)")
	}
}

func TestBillingService_HandleSubscriptionCancelled_CDC_ExpiredDeadline(t *testing.T) {
	svc, repo := newBillingSvc(false)

	orderID := uuid.New()
	invoiceID := uuid.New()
	deadline := time.Now().UTC().AddDate(0, 0, -3) // prazo expirado
	inv := &models.Invoice{
		ID:          invoiceID,
		OrderID:     orderID,
		Status:      models.StatusIssued,
		Amount:      99.0,
		CDCDeadline: &deadline,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	repo.invoices[invoiceID] = inv
	repo.byOrderID[orderID] = inv

	err := svc.HandleSubscriptionCancelled(context.Background(), models.SubscriptionCancelledEvent{
		OrderID: orderID.String(),
		Reason:  "cdc_art49",
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	// Prazo expirado: apenas cancelar sem RPS-D
	if repo.invoices[invoiceID].Status != models.StatusCancelled {
		t.Errorf("prazo CDC expirado: fatura deve ser cancelada, got %s", repo.invoices[invoiceID].Status)
	}
	if len(repo.invoices) != 1 {
		t.Error("prazo CDC expirado: não deve criar fatura de estorno")
	}
}

// ── RetryInvoice ──────────────────────────────────────────────────────────────

func TestBillingService_RetryInvoice_Success(t *testing.T) {
	svc, repo := newBillingSvc(false)

	invoiceID := uuid.New()
	inv := &models.Invoice{
		ID:                 invoiceID,
		OrderID:            uuid.New(),
		CustomerID:         uuid.New(),
		Amount:             99.0,
		ServiceDescription: "Serviço",
		Status:             models.StatusFailed,
		RPSType:            models.RPSTypeNormal,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	repo.invoices[invoiceID] = inv

	err := svc.RetryInvoice(context.Background(), invoiceID)
	if err != nil {
		t.Fatalf("erro inesperado no retry: %v", err)
	}
	// Após retry bem-sucedido, deve estar emitida
	if repo.invoices[invoiceID].Status != models.StatusIssued {
		t.Errorf("após retry bem-sucedido esperava status issued, got %s", repo.invoices[invoiceID].Status)
	}
}

func TestBillingService_RetryInvoice_NotFailed(t *testing.T) {
	svc, repo := newBillingSvc(false)

	invoiceID := uuid.New()
	inv := &models.Invoice{
		ID:      invoiceID,
		OrderID: uuid.New(),
		Status:  models.StatusIssued, // não está em falha
		Amount:  99.0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	repo.invoices[invoiceID] = inv

	err := svc.RetryInvoice(context.Background(), invoiceID)
	if err == nil {
		t.Fatal("esperava erro ao tentar retry de fatura não-falhada")
	}
}

// ── buildRPS — lógica de cálculo de imposto Simples Nacional ─────────────────

func TestBillingService_BuildRPS_ISSCalculo(t *testing.T) {
	svc, _ := newBillingSvc(false)

	// Alíquota: 2% (configurada em newBillingSvc)
	inv := &models.Invoice{
		ID:                 uuid.New(),
		OrderID:            uuid.New(),
		CustomerID:         uuid.New(),
		Amount:             100.0,
		ServiceDescription: "Serviço",
		RPSType:            models.RPSTypeNormal,
	}

	rps := svc.buildRPS(inv)

	if rps == nil {
		t.Fatal("esperava RPS não-nil")
	}
	expectedISS := 2.0 // 2% de 100
	if rps.ValorISS != expectedISS {
		t.Errorf("ISS calculado incorretamente: got %.2f, want %.2f", rps.ValorISS, expectedISS)
	}
	expectedLiquido := 98.0 // 100 - 2
	if rps.ValorLiquidoNfse != expectedLiquido {
		t.Errorf("valor líquido incorreto: got %.2f, want %.2f", rps.ValorLiquidoNfse, expectedLiquido)
	}
	if rps.Aliquota != 0.02 {
		t.Errorf("alíquota incorreta no RPS: got %.4f, want 0.02", rps.Aliquota)
	}
}

func TestBillingService_BuildRPS_TypeDevolucao(t *testing.T) {
	svc, _ := newBillingSvc(false)

	inv := &models.Invoice{
		ID:      uuid.New(),
		OrderID: uuid.New(),
		Amount:  50.0,
		RPSType: models.RPSTypeDevolucao,
	}

	rps := svc.buildRPS(inv)

	if rps.Tipo != nfse.RPSTypeRPSD {
		t.Errorf("tipo RPS-D incorreto: got %d, want %d", rps.Tipo, nfse.RPSTypeRPSD)
	}
}

func TestBillingService_BuildRPS_CodigoMunicipio(t *testing.T) {
	svc, _ := newBillingSvc(false)

	inv := &models.Invoice{
		ID:      uuid.New(),
		OrderID: uuid.New(),
		Amount:  100.0,
		RPSType: models.RPSTypeNormal,
	}

	rps := svc.buildRPS(inv)

	// Código IBGE de Salvador/BA
	if rps.CodigoMunicipio != 2927408 {
		t.Errorf("código município incorreto: got %d, want 2927408", rps.CodigoMunicipio)
	}
}

func TestBillingService_BuildRPS_ItemListaServico(t *testing.T) {
	svc, _ := newBillingSvc(false)

	inv := &models.Invoice{
		ID:      uuid.New(),
		OrderID: uuid.New(),
		Amount:  75.0,
		RPSType: models.RPSTypeNormal,
	}

	rps := svc.buildRPS(inv)

	// Item da lista configurado como "01.07"
	if rps.ItemListaServico != "01.07" {
		t.Errorf("item lista serviço incorreto: got %s, want 01.07", rps.ItemListaServico)
	}
}

// ── GetInvoice / ListInvoices ─────────────────────────────────────────────────

func TestBillingService_GetInvoice_Success(t *testing.T) {
	svc, repo := newBillingSvc(false)

	invoiceID := uuid.New()
	inv := &models.Invoice{
		ID:        invoiceID,
		OrderID:   uuid.New(),
		Amount:    99.0,
		Status:    models.StatusPending,
		RPSType:   models.RPSTypeNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	repo.invoices[invoiceID] = inv

	result, err := svc.GetInvoice(context.Background(), invoiceID)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if result.ID != invoiceID {
		t.Errorf("ID incorreto: got %v", result.ID)
	}
}

func TestBillingService_ListInvoices_Paginated(t *testing.T) {
	svc, repo := newBillingSvc(false)

	// Criar 5 faturas
	for i := 0; i < 5; i++ {
		id := uuid.New()
		repo.invoices[id] = &models.Invoice{
			ID:        id,
			OrderID:   uuid.New(),
			Amount:    float64(i+1) * 10.0,
			Status:    models.StatusPending,
			RPSType:   models.RPSTypeNormal,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
	}

	result, err := svc.ListInvoices(context.Background(), models.ListInvoicesFilter{
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("esperava 5 faturas, got %d", result.Total)
	}
	if result.Page != 1 {
		t.Errorf("página incorreta: got %d", result.Page)
	}
}
