package messaging

import (
	"context"
	"encoding/json"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"billing-service/internal/models"
	"billing-service/internal/service"
)

const (
	exchangeName = "iit.events"
	queueName    = "billing.payment.confirmed"
	routingKey   = "payment.confirmed"
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
			log.Println("[consumer] contexto cancelado — encerrando consumer")
			return
		default:
		}

		log.Printf("[consumer] conectando ao RabbitMQ...")
		if err := c.run(ctx); err != nil {
			log.Printf("[consumer] erro: %v — reconectando em %s", err, backoff)
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

// run estabelece conexão, declara exchange/queue e inicia consumo.
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

	// Declarar queue durable
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

	// Bind queue ao exchange com routing key payment.confirmed
	if err := ch.QueueBind(
		q.Name,
		routingKey,
		exchangeName,
		false,
		nil,
	); err != nil {
		return err
	}

	// QoS: processar uma mensagem por vez para evitar sobrecarga no webservice NFS-e
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}

	msgs, err := ch.Consume(
		q.Name,
		"billing-service",
		false, // auto-ack: false — ACK manual após processamento
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	log.Printf("[consumer] aguardando eventos em queue=%s routing_key=%s", queueName, routingKey)

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

		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			c.handleMessage(ctx, msg)
		}
	}
}

// handleMessage processa uma mensagem do RabbitMQ.
// Faz ACK em caso de sucesso ou erro de negócio (não retentável),
// e NACK com requeue=false em caso de erro de infraestrutura (vai para DLQ se configurada).
func (c *Consumer) handleMessage(ctx context.Context, msg amqp.Delivery) {
	// Sem logar body completo — pode conter dados sensíveis (R5)
	log.Printf("[consumer] mensagem recebida routing_key=%s delivery_tag=%d", msg.RoutingKey, msg.DeliveryTag)

	var event models.PaymentConfirmedEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		log.Printf("[consumer] mensagem malformada — descartando: %v", err)
		// ACK para evitar loop infinito com mensagem inválida
		_ = msg.Ack(false)
		return
	}

	if event.OrderID == "" || event.CustomerID == "" || event.Amount <= 0 {
		log.Printf("[consumer] evento inválido order_id=%q customer_id=%q amount=%.2f — descartando",
			event.OrderID, event.CustomerID, event.Amount)
		_ = msg.Ack(false)
		return
	}

	// Criar fatura e disparar processamento
	processCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	inv, err := c.billingService.CreateFromPaymentEvent(processCtx, event)
	if err != nil {
		log.Printf("[consumer] erro ao criar fatura order_id=%s: %v", event.OrderID, err)
		_ = msg.Nack(false, false) // não recolocar na fila — evitar loop
		return
	}

	if err := c.billingService.ProcessInvoice(processCtx, inv.ID); err != nil {
		log.Printf("[consumer] erro ao processar fatura id=%s: %v", inv.ID, err)
		// Fatura persiste como "failed" — retry disponível via API
	}

	_ = msg.Ack(false)
}
