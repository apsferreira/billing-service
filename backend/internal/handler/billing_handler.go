package handler

import (
	"errors"

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

// Health godoc
// GET /health
func (h *BillingHandler) Health(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":  "ok",
		"service": "billing-service",
	})
}
