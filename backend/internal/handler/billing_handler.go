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

// ListInvoices lista faturas com filtros opcionais.
//
//	@Summary		Listar faturas
//	@Tags			invoices
//	@Produce		json
//	@Param			status		query		string	false	"Filtro por status (pending|processing|emitted|failed|cancelled)"
//	@Param			customer_id	query		string	false	"UUID do cliente"
//	@Param			page		query		int		false	"Página (padrão 1)"
//	@Param			page_size	query		int		false	"Itens por página (padrão 20)"
//	@Success		200			{object}	models.PaginatedInvoices
//	@Failure		500			{object}	map[string]string
//	@Security		ServiceToken
//	@Router			/invoices [get]
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

// GetInvoice busca uma fatura pelo ID.
//
//	@Summary		Buscar fatura
//	@Tags			invoices
//	@Produce		json
//	@Param			id	path		string	true	"UUID da fatura"
//	@Success		200	{object}	models.Invoice
//	@Failure		400	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Security		ServiceToken
//	@Router			/invoices/{id} [get]
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

// RetryInvoice reprocessa uma fatura que falhou.
//
//	@Summary		Reprocessar fatura
//	@Tags			invoices
//	@Produce		json
//	@Param			id	path		string	true	"UUID da fatura"
//	@Success		200	{object}	map[string]string
//	@Failure		400	{object}	map[string]string
//	@Failure		409	{object}	map[string]string
//	@Failure		500	{object}	map[string]string
//	@Security		ServiceToken
//	@Router			/invoices/{id}/retry [post]
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

// CancelCDC cancela uma fatura dentro do prazo CDC Art. 49 (7 dias).
// Cria nota de devolução (RPS-D) e emite em background se dentro do prazo.
//
//	@Summary		Cancelar fatura por direito de arrependimento (CDC Art. 49)
//	@Tags			invoices
//	@Produce		json
//	@Param			id	path		string	true	"UUID da fatura"
//	@Success		200	{object}	map[string]interface{}
//	@Failure		400	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Failure		409	{object}	map[string]string
//	@Failure		422	{object}	map[string]string
//	@Security		ServiceToken
//	@Router			/invoices/{id}/cancel-cdc [post]
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

// Health verifica o status do serviço.
//
//	@Summary		Health check
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]string
//	@Router			/health [get]
func (h *BillingHandler) Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"service": "billing-service",
	})
}
