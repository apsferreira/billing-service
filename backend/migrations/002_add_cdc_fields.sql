-- =============================================================================
-- Migration 002: Campos para CDC Art. 49 (direito de arrependimento) e RPS-D
-- Serviço: billing-service
-- =============================================================================

-- UP

-- Tipo de RPS: "RPS" (nota normal) ou "RPS-D" (nota de devolução/estorno)
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS rps_type VARCHAR(10) NOT NULL DEFAULT 'RPS';

-- Prazo CDC Art. 49: created_at + 7 dias (apenas na primeira fatura do cliente)
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS cdc_deadline TIMESTAMPTZ;

-- Referência bidirecional entre nota original e nota de estorno
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS reversed_by_invoice_id UUID REFERENCES invoices(id);
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS original_invoice_id UUID REFERENCES invoices(id);

-- Constraint: rps_type deve ser um valor válido
ALTER TABLE invoices
    ADD CONSTRAINT chk_invoices_rps_type
    CHECK (rps_type IN ('RPS', 'RPS-D'));

-- Índice para facilitar busca de estornos por nota original
CREATE INDEX IF NOT EXISTS idx_invoices_original_invoice_id ON invoices(original_invoice_id);

-- =============================================================================
-- DOWN
-- ALTER TABLE invoices DROP CONSTRAINT IF EXISTS chk_invoices_rps_type;
-- DROP INDEX IF EXISTS idx_invoices_original_invoice_id;
-- ALTER TABLE invoices DROP COLUMN IF EXISTS original_invoice_id;
-- ALTER TABLE invoices DROP COLUMN IF EXISTS reversed_by_invoice_id;
-- ALTER TABLE invoices DROP COLUMN IF EXISTS cdc_deadline;
-- ALTER TABLE invoices DROP COLUMN IF EXISTS rps_type;
-- =============================================================================
