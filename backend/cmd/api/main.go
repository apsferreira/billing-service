package main

import (
	"context"
	"log"
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
	log.Println("PostgreSQL conectado")

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
	log.Println("Consumer RabbitMQ iniciado")

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

	// Handlers
	billingHandler := handler.NewBillingHandler(billingSvc)

	// Rotas
	app.Get("/health", billingHandler.Health)

	api := app.Group("/api/v1")
	api.Get("/invoices", billingHandler.ListInvoices)
	api.Get("/invoices/:id", billingHandler.GetInvoice)
	api.Post("/invoices/:id/retry", billingHandler.RetryInvoice)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("%s iniciando na porta %s (ambiente NFS-e: %s)", cfg.ServiceName, cfg.Port, cfg.NFSeEnvironment)
		if err := app.Listen(":" + cfg.Port); err != nil {
			log.Fatal("servidor encerrado com erro:", err)
		}
	}()

	<-quit
	log.Println("Encerrando servidor...")

	cancelConsumer()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Fatal("erro no shutdown do servidor:", err)
	}

	log.Println("Servidor encerrado")
}
