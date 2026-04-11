package messaging

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"billing-service/internal/models"
	"billing-service/internal/service"
)

const (
	exchangeName = "iit.events"

	queuePaymentConfirmed   = "billing.payment.confirmed"
	routingPaymentConfirmed = "payment.confirmed"

	queueSubscriptionCancelled   = "billing.subscription.cancelled"
	routingSubscriptionCancelled = "subscription.cancelled"
)

// Consumer consome eventos do RabbitMQ e aciona o BillingService.
type Consumer struct {
	url            string
	billingService *service.BillingService
}

// NewConsumer cria um novo consumidor de eventos.
func NewConsumer(url string, billingService *service.BillingService) *Consumer {
	return &Consumer{
		url:            url,
		billingService: billingService,
	}
}

// Start inicia o consumidor com reconexão automática usando backoff exponencial.
// Bloqueia até que o contexto seja cancelado.
func (c *Consumer) Start(ctx context.Context) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "contexto cancelado — encerrando consumer")
			return
		default:
		}

		slog.InfoContext(ctx, "conectando ao RabbitMQ")
		if err := c.run(ctx); err != nil {
			slog.WarnContext(ctx, "erro na conexão RabbitMQ — reconectando",
				slog.String("error", err.Error()),
				slog.String("backoff", backoff.String()),
			)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// run estabelece conexão, declara exchange/queues e inicia consumo.
func (c *Consumer) run(ctx context.Context) error {
	conn, err := amqp.Dial(c.url)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// Declarar exchange topic (idempotente — cria se não existir)
	if err := ch.ExchangeDeclare(
		exchangeName,
		"topic",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		return err
	}

	// Declarar e bindar queue de payment.confirmed
	if err := c.declareAndBind(ch, queuePaymentConfirmed, routingPaymentConfirmed); err != nil {
		return err
	}

	// Declarar e bindar queue de subscription.cancelled
	if err := c.declareAndBind(ch, queueSubscriptionCancelled, routingSubscriptionCancelled); err != nil {
		return err
	}

	// QoS: processar uma mensagem por vez para evitar sobrecarga no webservice NFS-e
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}

	// Consumir payment.confirmed
	msgsPayment, err := ch.Consume(
		queuePaymentConfirmed,
		"billing-service-payment",
		false, // auto-ack: false — ACK manual após processamento
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	// Consumir subscription.cancelled
	msgsCancelled, err := ch.Consume(
		queueSubscriptionCancelled,
		"billing-service-cancelled",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	slog.InfoContext(ctx, "aguardando eventos",
		slog.String("routing_key_1", routingPaymentConfirmed),
		slog.String("routing_key_2", routingSubscriptionCancelled),
	)

	// Monitorar fechamento da conexão
	connClosed := conn.NotifyClose(make(chan *amqp.Error, 1))

	for {
		select {
		case <-ctx.Done():
			return nil

		case amqpErr := <-connClosed:
			if amqpErr != nil {
				return amqpErr
			}
			return nil

		case msg, ok := <-msgsPayment:
			if !ok {
				return nil
			}
			c.handlePaymentConfirmed(ctx, msg)

		case msg, ok := <-msgsCancelled:
			if !ok {
				return nil
			}
			c.handleSubscriptionCancelled(ctx, msg)
		}
	}
}

// declareAndBind declara uma queue durable e a vincula ao exchange com a routing key informada.
func (c *Consumer) declareAndBind(ch *amqp.Channel, queueName, routingKey string) error {
	q, err := ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	)
	if err != nil {
		return err
	}

	return ch.QueueBind(
		q.Name,
		routingKey,
		exchangeName,
		false,
		nil,
	)
}

// handlePaymentConfirmed processa o evento payment.confirmed.
// Faz ACK em caso de sucesso ou erro de negócio (não retentável),
// e NACK com requeue=false em caso de erro de infraestrutura.
func (c *Consumer) handlePaymentConfirmed(ctx context.Context, msg amqp.Delivery) {
	// Sem logar body completo — pode conter dados sensíveis (R5)
	slog.InfoContext(ctx, "mensagem recebida",
		slog.String("routing_key", msg.RoutingKey),
		slog.Uint64("delivery_tag", msg.DeliveryTag),
	)

	// O checkout-service publica no iit.events com envelope:
	// {"event": "payment.confirmed", "occurred_at": "...", "data": {...}}
	// Tentamos extrair do envelope primeiro; fallback para payload direto (retrocompatibilidade).
	var event models.PaymentConfirmedEvent

	var envelope struct {
		Event string                       `json:"event"`
		Data  models.PaymentConfirmedEvent `json:"data"`
	}
	if err := json.Unmarshal(msg.Body, &envelope); err == nil && envelope.Data.OrderID != "" {
		event = envelope.Data
		// Converter amount de float (reais) do checkout para float do billing
		// O checkout envia Amount como float64 (ex: 39.00 = R$39)
	} else {
		// Fallback: payload direto (sem envelope)
		if err := json.Unmarshal(msg.Body, &event); err != nil {
			slog.WarnContext(ctx, "mensagem malformada em payment.confirmed — descartando",
				slog.String("error", err.Error()),
			)
			_ = msg.Ack(false)
			return
		}
	}

	if event.OrderID == "" || event.CustomerID == "" || event.Amount <= 0 {
		slog.WarnContext(ctx, "evento payment.confirmed inválido — descartando")
		_ = msg.Ack(false)
		return
	}

	processCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	inv, err := c.billingService.CreateFromPaymentEvent(processCtx, event)
	if err != nil {
		slog.ErrorContext(ctx, "erro ao criar fatura",
			slog.String("order_id", event.OrderID),
			slog.String("error", err.Error()),
		)
		_ = msg.Nack(false, false) // não recolocar na fila — evitar loop
		return
	}

	if err := c.billingService.ProcessInvoice(processCtx, inv.ID); err != nil {
		slog.WarnContext(ctx, "erro ao processar fatura",
			slog.String("invoice_id", inv.ID.String()),
			slog.String("error", err.Error()),
		)
		// Fatura persiste como "failed" — retry disponível via API
	}

	_ = msg.Ack(false)
}

// handleSubscriptionCancelled processa o evento subscription.cancelled.
// Se o motivo for "cdc_art49" e o prazo CDC ainda estiver vigente, cria RPS-D.
// Caso contrário, apenas cancela a fatura.
func (c *Consumer) handleSubscriptionCancelled(ctx context.Context, msg amqp.Delivery) {
	slog.InfoContext(ctx, "mensagem recebida",
		slog.String("routing_key", msg.RoutingKey),
		slog.Uint64("delivery_tag", msg.DeliveryTag),
	)

	var event models.SubscriptionCancelledEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		slog.WarnContext(ctx, "mensagem malformada em subscription.cancelled — descartando",
			slog.String("error", err.Error()),
		)
		_ = msg.Ack(false)
		return
	}

	if event.OrderID == "" || event.CustomerID == "" {
		slog.WarnContext(ctx, "evento subscription.cancelled inválido — descartando")
		_ = msg.Ack(false)
		return
	}

	processCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := c.billingService.HandleSubscriptionCancelled(processCtx, event); err != nil {
		slog.ErrorContext(ctx, "erro ao processar cancelamento",
			slog.String("order_id", event.OrderID),
			slog.String("error", err.Error()),
		)
		_ = msg.Nack(false, false)
		return
	}

	_ = msg.Ack(false)
}
