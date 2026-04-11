package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

// ErrorResponse é o formato padrão de erro da API.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// ErrorHandler trata erros não capturados nos handlers.
// Nunca expõe stack traces ou detalhes internos ao cliente.
func ErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := "erro interno do servidor"

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		msg = e.Message
	}

	// Logar sem dados sensíveis (R5)
	slog.Error("requisição com erro",
		slog.Int("status", code),
		slog.String("path", c.Path()),
		slog.String("method", c.Method()),
		slog.String("error", err.Error()),
	)

	return c.Status(code).JSON(ErrorResponse{
		Error:   http.StatusText(code),
		Message: msg,
	})
}
