# billing-service

Serviço de emissão automática de NFS-e (Nota Fiscal de Serviços Eletrônica) do ecossistema IIT, seguindo o padrão ABRASF e o sistema ISS da Prefeitura de Salvador. Consome eventos de pagamento confirmado do RabbitMQ (`iit.events`) e emite notas fiscais via integração com o webservice municipal, com suporte a RPS-D (nota de devolução) para exercício do direito de arrependimento (CDC Art. 49).

## Stack

| Camada | Tecnologia |
|--------|-----------|
| Backend | Go 1.24 + Fiber v2 |
| Banco de dados | PostgreSQL 16 (pgx/v5) |
| Mensageria | RabbitMQ — consumer de `iit.events` (amqp091-go) |
| NFS-e | ABRASF + certificado A1 (.pfx) — ISS Salvador |
| Build | Multi-stage Docker |

## Arquitetura

O billing-service é um **consumer event-driven**. Não possui frontend e não é exposto via Cloudflare Tunnel — opera exclusivamente a partir de eventos publicados pelo checkout-service.

```
checkout-service ──(AMQP iit.events)──► billing-service ──► Prefeitura Salvador (ABRASF)
                                               │
                                               └──► PostgreSQL (invoices)
```

A API HTTP interna (protegida por `SERVICE_TOKEN`) permite consulta e operações manuais por outros serviços do ecossistema.

## Endpoints

Todas as rotas (exceto `/health`) exigem o header `Authorization: Bearer <SERVICE_TOKEN>`.

| Método | Rota | Descrição |
|--------|------|-----------|
| `GET` | `/health` | Health check |
| `GET` | `/api/v1/invoices` | Listar notas fiscais |
| `GET` | `/api/v1/invoices/:id` | Buscar nota fiscal por ID |
| `POST` | `/api/v1/invoices/:id/retry` | Reprocessar emissão de nota com falha |
| `POST` | `/api/v1/invoices/:id/cancel-cdc` | Cancelar nota (CDC Art. 49 — direito de arrependimento) |

## Modelo de dados

### `invoices`

| Coluna | Tipo | Descrição |
|--------|------|-----------|
| `id` | UUID | Chave primária |
| `order_id` | UUID | ID do pedido no checkout-service |
| `customer_id` | UUID | ID do cliente no customer-service |
| `tenant_id` | UUID | Tenant proprietário da nota |
| `amount` | DECIMAL(10,2) | Valor da nota em reais |
| `service_description` | TEXT | Descrição do serviço na nota |
| `status` | VARCHAR(50) | `pending`, `processing`, `issued`, `failed`, `cancelled` |
| `rps_type` | VARCHAR(10) | `RPS` (nota normal) ou `RPS-D` (devolução/estorno) |
| `nfse_number` | VARCHAR(100) | Número da NFS-e emitida |
| `nfse_code` | VARCHAR(100) | Código de verificação da NFS-e |
| `nfse_xml` | TEXT | XML completo da NFS-e |
| `nfse_pdf_url` | TEXT | URL do PDF no MinIO |
| `cdc_deadline` | TIMESTAMPTZ | Prazo CDC Art. 49 (created_at + 7 dias) |
| `original_invoice_id` | UUID | FK para a nota original (em caso de RPS-D) |
| `reversed_by_invoice_id` | UUID | FK para a nota de estorno (se revertida) |
| `error_message` | TEXT | Mensagem de erro quando `status = failed` |
| `attempts` | INT | Número de tentativas de emissão |
| `issued_at` | TIMESTAMPTZ | Timestamp da emissão bem-sucedida |

## Variáveis de ambiente

| Variável | Obrigatória | Padrão | Descrição |
|----------|-------------|--------|-----------|
| `DATABASE_URL` | Sim | — | URL de conexão PostgreSQL |
| `RABBITMQ_URL` | Sim | — | URL AMQP para consumir eventos |
| `SERVICE_TOKEN` | Sim | — | Token Bearer para rotas internas |
| `NFSE_ENDPOINT_URL` | Sim | — | URL do webservice ABRASF da Prefeitura |
| `NFSE_ENVIRONMENT` | Não | `homologacao` | `homologacao` ou `producao` |
| `NFSE_PROVIDER_CNPJ` | Sim | — | CNPJ do prestador de serviços |
| `NFSE_PROVIDER_IM` | Sim | — | Inscrição Municipal do prestador |
| `NFSE_CERT_PATH` | Sim | — | Caminho para o certificado A1 (.pfx) |
| `NFSE_CERT_PASSWORD` | Sim | — | Senha do certificado A1 |
| `NFSE_ALIQUOTA` | Não | `5.00` | Alíquota ISS em % |
| `NFSE_ITEM_LISTA` | Não | `1.07` | Código do item na lista de serviços |
| `PORT` | Não | `3070` | Porta do servidor HTTP |
| `SERVICE_NAME` | Não | `billing-service` | Nome do serviço nos logs |
| `SERVICE_VERSION` | Não | `1.0.0` | Versão do serviço |

## Desenvolvimento local

### Pré-requisitos

- Docker e Docker Compose
- shared-infra rodando (`cd ../shared-infra && docker compose up -d`)
- RabbitMQ disponível (necessário para o consumer funcionar)
- Certificado A1 (.pfx) para ambiente de homologação

### Subir o serviço

```bash
cd backend
make dev

# Ou via Docker Compose na raiz
docker compose up -d
```

### Rodar migrations

```bash
cd backend
psql $DATABASE_URL -f migrations/001_create_invoices.sql
psql $DATABASE_URL -f migrations/002_add_cdc_fields.sql
```

### Variáveis mínimas para dev

```env
DATABASE_URL=postgres://postgres:postgres@localhost:5432/billing_db
RABBITMQ_URL=amqp://guest:guest@localhost:5672/
SERVICE_TOKEN=dev-token
NFSE_ENDPOINT_URL=https://nfse-homologacao.salvador.ba.gov.br/ws
NFSE_ENVIRONMENT=homologacao
NFSE_PROVIDER_CNPJ=00000000000000
NFSE_PROVIDER_IM=000000
NFSE_CERT_PATH=/certs/certificado.pfx
NFSE_CERT_PASSWORD=senha-do-certificado
PORT=3070
```

### Certificado A1

O arquivo `.pfx` deve ser montado via volume no container. Em produção o segredo é injetado via Kubernetes Secret:

```yaml
volumeMounts:
  - name: nfse-cert
    mountPath: /certs
    readOnly: true
```

## Estrutura do projeto

```
billing-service/
  backend/
    cmd/api/            # main.go — inicialização, consumer, rotas
    internal/
      config/           # carregamento de configuração via env
      handler/          # handlers HTTP
      messaging/        # consumer RabbitMQ (iit.events)
      middleware/       # security headers, error handler
      models/           # structs e tipos
      nfse/             # cliente ABRASF (assina XML, chama webservice)
      repository/       # queries SQL (prepared statements)
      service/          # lógica de emissão e cancelamento de NFS-e
    migrations/         # SQL sequencial com DOWN comentado
  k8s/                  # manifests Kubernetes
  docker-compose.yml    # dev local
  26175564.pfx          # certificado A1 (NÃO commitar em repos públicos)
```

## Ciclo de vida de uma nota fiscal

1. checkout-service publica `payment.confirmed` no RabbitMQ (`iit.events`)
2. billing-service consome o evento e cria um registro `invoices` com `status = pending`
3. O serviço chama o webservice ABRASF com o XML assinado pelo certificado A1
4. Em caso de sucesso: `status = issued`, `nfse_number` e `nfse_xml` preenchidos
5. Em caso de falha: `status = failed`, `error_message` registrado, retry disponível via API

### Cancelamento CDC Art. 49

Quando o cliente solicita cancelamento dentro de 7 dias da compra:

1. Chamada a `POST /api/v1/invoices/:id/cancel-cdc`
2. billing-service emite uma RPS-D (nota de devolução) referenciando a nota original
3. `original_invoice_id` da nota de estorno aponta para a nota cancelada
4. `reversed_by_invoice_id` da nota original aponta para a nota de estorno

## Deploy (K3s)

O billing-service roda como workload interno sem ingress público. Não é exposto via Cloudflare Tunnel — opera exclusivamente via consumer RabbitMQ e chamadas internas de outros serviços.

```bash
# Build da imagem
docker build -f backend/Dockerfile -t ghcr.io/apsferreira/billing-service:latest .

# Push (CI/CD faz automaticamente via GitHub Actions)
docker push ghcr.io/apsferreira/billing-service:latest
```

Manifestos K8s em `k8s/`.

> **Atencao:** O certificado A1 (`.pfx`) nunca deve ser commitado em repositórios públicos. Usar Kubernetes Secret ou secret manager para injetar em produção.
