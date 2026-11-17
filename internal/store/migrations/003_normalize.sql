-- Campos unificados entre fuentes + ID público + guardado incremental.
ALTER TABLE auctions ADD COLUMN province_raw TEXT;
ALTER TABLE auctions ADD COLUMN province_code TEXT;
ALTER TABLE auctions ADD COLUMN status_code TEXT;
ALTER TABLE auctions ADD COLUMN asset_subtype TEXT;
ALTER TABLE auctions ADD COLUMN detailed_at TEXT;
ALTER TABLE auctions ADD COLUMN public_id TEXT GENERATED ALWAYS AS ('SB-' || printf('%06d', id)) VIRTUAL;

-- Todo lo guardado hasta ahora se descargó con detalle completo.
UPDATE auctions SET detailed_at = last_seen;

CREATE INDEX idx_auctions_province ON auctions (active, province_code);
CREATE INDEX idx_auctions_subtype ON auctions (active, asset_subtype);
CREATE INDEX idx_auctions_status ON auctions (active, status_code);

DROP VIEW auctions_v;
CREATE VIEW auctions_v AS
SELECT a.*,
  100.0 * claimed_cents / NULLIF(valuation_cents, 0) AS pct_claimed_valuation,
  100.0 * claimed_cents / NULLIF(appraisal_cents, 0) AS pct_claimed_appraisal,
  100.0 * deposit_cents / NULLIF(valuation_cents, 0) AS pct_deposit_valuation
FROM auctions a;
