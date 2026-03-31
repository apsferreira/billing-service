package handler

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"billing-service/internal/models"
	"billing-service/internal/service"
)

// BillingHandler expõe os endpoints HTTP do billing-service.
type BillingHandler struct {
	svc *service.BillingService
}

// NewBillingHandler cria um novo handler de faturamento.
func NewBillingHandler(svc *service.BillingService) *BillingHandler {
	return &BillingHandler{svc: svc}
}

// ListInvoices godoc
// GET /invoices
// Query: status, customer_id, page, page_size
func (h *BillingHandler) ListInvoices(c *fiber.Ctx) error {
	filter := models.ListInvoicesFilter{
		Status:     models.InvoiceStatus(c.Query("status")),
		CustomerID: c.Query("customer_id"),
		Page:       c.QueryInt("page", 1),
		PageSize:   c.QueryInt("page_size", 20),
	}

	result, err := h.svc.ListInvoices(c.Context(), filter)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "erro ao listar faturas")
	}

	return c.JSON(result)
}

// GetInvoice godoc
// GET /invoices/:id
func (h *BillingHandler) GetInvoice(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "ID de fatura inválido")
	}

	inv, err := h.svc.GetInvoice(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "fatura não encontrada")
	}

	return c.JSON(inv)
}

// RetryInvoice godoc
// POST /invoices/:id/retry
// Reprocessa uma fatura que falhou.
func (h *BillingHandler) RetryInvoice(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "ID de fatura inválido")
	}

	if err := h.svc.RetryInvoice(c.Context(), id); err != nil {
		var invTransition *service.ErrInvalidTransition
		if errors.As(err, &invTransition) {
			return fiber.NewError(fiber.StatusConflict, err.Error())
		}
		return fiber.NewError(fiber.StatusInternalServerError, "erro ao reprocessar fatura")
	}

	return c.JSON(fiber.Map{
		"message": "fatura enviada para reprocessamento",
	})
}

// CancelCDC godoc
// POST /invoices/:id/cancel-cdc
// Cancela uma fatura dentro do prazo CDC Art. 49 (7 dias da primeira contratação).
// Se o prazo ainda estiver vigente, cria uma nota de devolução (RPS-D) e a emite em background.
func (h *BillingHandler) CancelCDC(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "ID de fatura inválido")
	}

	inv, err := h.svc.GetInvoice(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "fatura não encontrada")
	}

	if inv.CDCDeadline == nil {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "esta fatura não possui prazo CDC — direito de arrependimento não aplicável")
	}

	if c.Context().Time().After(*inv.CDCDeadline) {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "prazo CDC expirado")
	}

	reversal, err := h.svc.CreateReversal(c.Context(), id, "cdc_art49")
	if err != nil {
		var alreadyReversed *service.ErrAlreadyReversed
		if errors.As(err, &alreadyReversed) {
			return fiber.NewError(fiber.StatusConflict, err.Error())
		}
		return fiber.NewError(fiber.StatusInternalServerError, "erro ao criar estorno")
	}

	// Emissão do RPS-D em background para não bloquear a resposta HTTP
	reversalID := reversal.ID
	go func() {
		emitCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := h.svc.ProcessInvoice(emitCtx, reversalID); err != nil {
			// Erro já logado dentro de ProcessInvoice
			_ = err
		}
	}()

	return c.JSON(fiber.Map{
		"original_invoice_id": id,
		"reversal_invoice_id": reversal.ID,
		"status":              "reversal_pending",
	})
}

// Health godoc
// GET /health
func (h *BillingHandler) Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"service": "billing-service",
	})
}
