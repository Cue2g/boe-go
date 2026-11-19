-- Localidad de las subastas de la Seguridad Social (regulares): se extrae de la
-- dirección en el hook Go de esta migración (backfill.go).
CREATE INDEX IF NOT EXISTS idx_auctions_locality ON auctions (active, locality);
