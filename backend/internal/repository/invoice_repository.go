package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"billing-service/internal/models"
)

// InvoiceRepository gerencia persistência de faturas no PostgreSQL.
// Todas as queries usam prepared statements com parâmetros $N (R1).
type InvoiceRepository struct {
	db *pgxpool.Pool
}

// NewInvoiceRepository cria um novo repositório de faturas.
func NewInvoiceRepository(db *pgxpool.Pool) *InvoiceRepository {
	return &InvoiceRepository{db: db}
}

// Create insere uma nova fatura com status pending.
func (r *InvoiceRepository) Create(ctx context.Context, invoice *models.Invoice) error {
	query := `
		INSERT INTO invoices (
			id, order_id, customer_id, tenant_id,
			amount, service_description, status,
			rps_type, cdc_deadline, original_invoice_id,
			attempts, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10,
			$11, $12, $13
		)`

	now := time.Now().UTC()
	invoice.CreatedAt = now
	invoice.UpdatedAt = now

	_, err := r.db.Exec(ctx, query,
		invoice.ID,
		invoice.OrderID,
		invoice.CustomerID,
		invoice.TenantID,
		invoice.Amount,
		invoice.ServiceDescription,
		invoice.Status,
		string(invoice.RPSType),
		invoice.CDCDeadline,
		invoice.OriginalInvoiceID,
		invoice.Attempts,
		invoice.CreatedAt,
		invoice.UpdatedAt,
	)
	return err
}

// GetByID busca uma fatura pelo ID.
func (r *InvoiceRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Invoice, error) {
	query := `
		SELECT
			id, order_id, customer_id, tenant_id,
			amount, service_description, status,
			rps_type, cdc_deadline,
			reversed_by_invoice_id, original_invoice_id,
			nfse_number, nfse_code, nfse_xml, nfse_pdf_url,
			error_message, attempts, issued_at,
			created_at, updated_at
		FROM invoices
		WHERE id = $1`

	inv := &models.Invoice{}
	err := r.db.QueryRow(ctx, query, id).Scan(
		&inv.ID, &inv.OrderID, &inv.CustomerID, &inv.TenantID,
		&inv.Amount, &inv.ServiceDescription, &inv.Status,
		&inv.RPSType, &inv.CDCDeadline,
		&inv.ReversedByInvoiceID, &inv.OriginalInvoiceID,
		&inv.NfseNumber, &inv.NfseCode, &inv.NfseXML, &inv.NfsePDFURL,
		&inv.ErrorMessage, &inv.Attempts, &inv.IssuedAt,
		&inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// GetByOrderID busca a fatura original (RPS) associada a um pedido.
// Retorna apenas a nota normal, não estornos (RPS-D).
func (r *InvoiceRepository) GetByOrderID(ctx context.Context, orderID uuid.UUID) (*models.Invoice, error) {
	query := `
		SELECT
			id, order_id, customer_id, tenant_id,
			amount, service_description, status,
			rps_type, cdc_deadline,
			reversed_by_invoice_id, original_invoice_id,
			nfse_number, nfse_code, nfse_xml, nfse_pdf_url,
			error_message, attempts, issued_at,
			created_at, updated_at
		FROM invoices
		WHERE order_id = $1 AND rps_type = $2
		LIMIT 1`

	inv := &models.Invoice{}
	err := r.db.QueryRow(ctx, query, orderID, string(models.RPSTypeNormal)).Scan(
		&inv.ID, &inv.OrderID, &inv.CustomerID, &inv.TenantID,
		&inv.Amount, &inv.ServiceDescription, &inv.Status,
		&inv.RPSType, &inv.CDCDeadline,
		&inv.ReversedByInvoiceID, &inv.OriginalInvoiceID,
		&inv.NfseNumber, &inv.NfseCode, &inv.NfseXML, &inv.NfsePDFURL,
		&inv.ErrorMessage, &inv.Attempts, &inv.IssuedAt,
		&inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// CountIssuedByCustomer retorna quantas faturas do tipo RPS (não estornos) foram emitidas
// com sucesso para o cliente. Usado para determinar se é a primeira contratação (CDC Art. 49).
func (r *InvoiceRepository) CountIssuedByCustomer(ctx context.Context, customerID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM invoices
		WHERE customer_id = $1
		  AND rps_type = $2
		  AND status = $3`

	var count int
	err := r.db.QueryRow(ctx, query, customerID, string(models.RPSTypeNormal), string(models.StatusIssued)).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// List retorna faturas paginadas com filtros opcionais.
func (r *InvoiceRepository) List(ctx context.Context, filter models.ListInvoicesFilter) ([]models.Invoice, int, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	offset := (filter.Page - 1) * filter.PageSize

	// Construção segura da query com prepared statements
	args := []interface{}{}
	argIdx := 1
	where := "WHERE 1=1"

	if filter.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, string(filter.Status))
		argIdx++
	}
	if filter.CustomerID != "" {
		where += fmt.Sprintf(" AND customer_id = $%d", argIdx)
		args = append(args, filter.CustomerID)
		argIdx++
	}

	countQuery := "SELECT COUNT(*) FROM invoices " + where
	var total int
	if err := r.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, filter.PageSize, offset)
	dataQuery := fmt.Sprintf(`
		SELECT
			id, order_id, customer_id, tenant_id,
			amount, service_description, status,
			rps_type, cdc_deadline,
			reversed_by_invoice_id, original_invoice_id,
			nfse_number, nfse_code, nfse_xml, nfse_pdf_url,
			error_message, attempts, issued_at,
			created_at, updated_at
		FROM invoices
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1)

	rows, err := r.db.Query(ctx, dataQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var invoices []models.Invoice
	for rows.Next() {
		var inv models.Invoice
		if err := rows.Scan(
			&inv.ID, &inv.OrderID, &inv.CustomerID, &inv.TenantID,
			&inv.Amount, &inv.ServiceDescription, &inv.Status,
			&inv.RPSType, &inv.CDCDeadline,
			&inv.ReversedByInvoiceID, &inv.OriginalInvoiceID,
			&inv.NfseNumber, &inv.NfseCode, &inv.NfseXML, &inv.NfsePDFURL,
			&inv.ErrorMessage, &inv.Attempts, &inv.IssuedAt,
			&inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		invoices = append(invoices, inv)
	}

	return invoices, total, nil
}

// allowedInvoiceFields define quais colunas podem ser atualizadas dinamicamente.
// BKL-115: allowlist previne SQL injection via chaves do map.
var allowedInvoiceFields = map[string]bool{
	"status":         true,
	"nfse_number":    true,
	"nfse_pdf_url":   true,
	"nfse_xml_url":   true,
	"issued_at":      true,
	"error_message":  true,
	"updated_at":     true,
}

// UpdateStatus atualiza o status e campos relacionados à emissão da NFS-e.
func (r *InvoiceRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status models.InvoiceStatus, fields map[string]interface{}) error {
	fields["status"] = string(status)
	fields["updated_at"] = time.Now().UTC()

	// BKL-115: validar cada coluna contra allowlist antes de usar no SQL
	setClause := ""
	args := []interface{}{}
	argIdx := 1

	for k, v := range fields {
		if !allowedInvoiceFields[k] {
			return fmt.Errorf("campo não permitido para atualização: %s", k)
		}
		if setClause != "" {
			setClause += ", "
		}
		setClause += fmt.Sprintf("%s = $%d", k, argIdx)
		args = append(args, v)
		argIdx++
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE invoices SET %s WHERE id = $%d", setClause, argIdx)

	_, err := r.db.Exec(ctx, query, args...)
	return err
}

// IncrementAttempts incrementa o contador de tentativas de emissão.
func (r *InvoiceRepository) IncrementAttempts(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE invoices SET attempts = attempts + 1, updated_at = $1 WHERE id = $2`
	_, err := r.db.Exec(ctx, query, time.Now().UTC(), id)
	return err
}

// CreateReversalAtomic cria a fatura de estorno e vincula à nota original em uma única
// transação pgx. BKL-968: previne estado inconsistente (estorno órfão) em caso de falha parcial.
func (r *InvoiceRepository) CreateReversalAtomic(ctx context.Context, reversal *models.Invoice, originalID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("falha ao iniciar transação: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	insertQuery := `
		INSERT INTO invoices (
			id, order_id, customer_id, tenant_id,
			amount, service_description, status,
			rps_type, cdc_deadline, original_invoice_id,
			attempts, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10,
			$11, $12, $13
		)`
	now := time.Now().UTC()
	reversal.CreatedAt = now
	reversal.UpdatedAt = now

	if _, err = tx.Exec(ctx, insertQuery,
		reversal.ID,
		reversal.OrderID,
		reversal.CustomerID,
		reversal.TenantID,
		reversal.Amount,
		reversal.ServiceDescription,
		reversal.Status,
		string(reversal.RPSType),
		reversal.CDCDeadline,
		reversal.OriginalInvoiceID,
		reversal.Attempts,
		reversal.CreatedAt,
		reversal.UpdatedAt,
	); err != nil {
		return fmt.Errorf("erro ao inserir fatura de estorno: %w", err)
	}

	// Vincular nota original ao estorno — deve ser atômica com o insert
	updateQuery := `UPDATE invoices SET reversed_by_invoice_id = $1, updated_at = $2 WHERE id = $3`
	if _, err = tx.Exec(ctx, updateQuery, reversal.ID, now, originalID); err != nil {
		return fmt.Errorf("erro ao vincular estorno à nota original: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("falha ao commitar transação de estorno: %w", err)
	}
	return nil
}
