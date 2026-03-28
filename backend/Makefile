## billing-service — Makefile
## Todos os builds Go via Docker (nunca instalar Go localmente)

.PHONY: up down build test migrate lint

# Inicia serviços de desenvolvimento (shared-infra deve estar UP)
up:
	docker compose -f ../docker-compose.yml up -d

# Para serviços de desenvolvimento
down:
	docker compose -f ../docker-compose.yml down

# Build da imagem Docker
build:
	docker build -t billing-service:local .

# Executa testes unitários
test:
	docker run --rm \
		-v $(PWD):/app \
		-w /app \
		golang:1.24-alpine \
		sh -c "GOTOOLCHAIN=auto go test ./... -v -cover"

# Aplica migrations no banco de dados
# DATABASE_URL deve estar definida
migrate:
	@if [ -z "$(DATABASE_URL)" ]; then \
		echo "ERRO: DATABASE_URL não definida"; exit 1; \
	fi
	for f in migrations/*.sql; do \
		echo "Aplicando $$f..."; \
		docker run --rm \
			--network host \
			-e PGPASSWORD=$$(echo $(DATABASE_URL) | sed 's/.*:\(.*\)@.*/\1/') \
			-v $(PWD)/$$f:/migration.sql \
			postgres:15-alpine \
			psql "$(DATABASE_URL)" -f /migration.sql; \
	done

# Linter
lint:
	docker run --rm \
		-v $(PWD):/app \
		-w /app \
		golangci/golangci-lint:latest \
		golangci-lint run ./... --timeout=3m
