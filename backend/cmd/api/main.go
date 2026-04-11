package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/jackc/pgx/v5/pgxpool"

	"billing-service/internal/config"
	"billing-service/internal/handler"
	"billing-service/internal/messaging"
	"billing-service/internal/middleware"
	"billing-service/internal/nfse"
	"billing-service/internal/repository"
	"billing-service/internal/service"
)

// serviceTokenAuth valida o Bearer token de serviço nas rotas internas (SEC-009).
func serviceTokenAuth(token string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if token == "" {
			// SERVICE_TOKEN não configurado — bloqueia tudo por segurança
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		auth := c.Get("Authorization")
		if auth != "Bearer "+token {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
		}
		return c.Next()
	}
}

func main() {
	cfg := config.Load()

	// Conexão com PostgreSQL
	dbPool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("falha ao conectar ao banco de dados: %v", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(context.Background()); err != nil {
		log.Fatalf("falha ao verificar conexão com banco de dados: %v", err)
	}
	slog.Info("PostgreSQL conectado")

	// Dependências
	invoiceRepo := repository.NewInvoiceRepository(dbPool)

	nfseClient, err := nfse.NewNFSeClient(
		cfg.NFSeEndpointURL,
		cfg.NFSeEnvironment,
		cfg.NFSeProviderCNPJ,
		cfg.NFSeProviderIM,
		cfg.NFSeCertPath,
		cfg.NFSeCertPassword,
	)
	if err != nil {
		log.Fatalf("falha ao inicializar cliente NFS-e: %v", err)
	}

	billingSvc := service.NewBillingService(invoiceRepo, nfseClient, cfg.NFSeAliquota, cfg.NFSeItemLista)

	// Consumer RabbitMQ — inicia em goroutine separada
	consumer := messaging.NewConsumer(cfg.RabbitMQURL, billingSvc)
	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	defer cancelConsumer()

	go consumer.Start(consumerCtx)
	slog.Info("Consumer RabbitMQ iniciado")

	// Fiber
	app := fiber.New(fiber.Config{
		AppName:      cfg.ServiceName + " v" + cfg.ServiceVersion,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorHandler: middleware.ErrorHandler,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		// Não logar query strings completas — podem conter dados sensíveis (R5)
		Format: "[${time}] ${status} ${method} ${path} ${latency}\n",
	}))
	app.Use(middleware.SecurityHeaders())

	// Handlers
	billingHandler := handler.NewBillingHandler(billingSvc)

	// Rotas
	app.Get("/health", billingHandler.Health)

	api := app.Group("/api/v1", serviceTokenAuth(cfg.ServiceToken))
	api.Get("/invoices", billingHandler.ListInvoices)
	api.Get("/invoices/:id", billingHandler.GetInvoice)
	api.Post("/invoices/:id/retry", billingHandler.RetryInvoice)
	api.Post("/invoices/:id/cancel-cdc", billingHandler.CancelCDC)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("servidor iniciando",
			slog.String("service", cfg.ServiceName),
			slog.String("port", cfg.Port),
			slog.String("nfse_environment", cfg.NFSeEnvironment),
		)
		if err := app.Listen(":" + cfg.Port); err != nil {
			log.Fatal("servidor encerrado com erro:", err)
		}
	}()

	<-quit
	slog.Info("encerrando servidor")

	cancelConsumer()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Fatal("erro no shutdown do servidor:", err)
	}

	slog.Info("servidor encerrado")
}
