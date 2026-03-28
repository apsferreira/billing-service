-- =============================================================================
-- Migration 001: Tabela de faturas (NFS-e)
-- Serviço: billing-service
-- =============================================================================

-- UP

CREATE TABLE IF NOT EXISTS invoices (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL,
    customer_id         UUID NOT NULL,
    tenant_id           UUID,
    amount              DECIMAL(10,2) NOT NULL,
    service_description TEXT NOT NULL DEFAULT 'Serviços de tecnologia',
    status              VARCHAR(50) NOT NULL DEFAULT 'pending',
    -- Valores válidos: pending | processing | issued | failed | cancelled
    nfse_number         VARCHAR(100),
    nfse_code           VARCHAR(100),    -- código de verificação da NFS-e
    nfse_xml            TEXT,            -- XML completo da NFS-e emitida
    nfse_pdf_url        TEXT,            -- URL do PDF no MinIO
    error_message       TEXT,            -- mensagem de erro se status = failed
    attempts            INT NOT NULL DEFAULT 0,
    issued_at           TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invoices_order_id     ON invoices(order_id);
CREATE INDEX IF NOT EXISTS idx_invoices_customer_id  ON invoices(customer_id);
CREATE INDEX IF NOT EXISTS idx_invoices_status       ON invoices(status);
CREATE INDEX IF NOT EXISTS idx_invoices_created_at   ON invoices(created_at DESC);

-- Constraint: status deve ser um valor válido
ALTER TABLE invoices
    ADD CONSTRAINT chk_invoices_status
    CHECK (status IN ('pending', 'processing', 'issued', 'failed', 'cancelled'));

-- Constraint: amount deve ser positivo
ALTER TABLE invoices
    ADD CONSTRAINT chk_invoices_amount_positive
    CHECK (amount > 0);

-- =============================================================================
-- DOWN
-- DROP TABLE IF EXISTS invoices;
-- =============================================================================
