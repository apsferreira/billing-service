package middleware

import (
	"log"
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
	log.Printf("[error_handler] status=%d path=%s method=%s err=%v",
		code, c.Path(), c.Method(), err)

	return c.Status(code).JSON(ErrorResponse{
		Error:   http.StatusText(code),
		Message: msg,
	})
}
