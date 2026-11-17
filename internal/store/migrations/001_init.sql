CREATE TABLE auctions (
  id              INTEGER PRIMARY KEY,
  source          TEXT NOT NULL,
  external_id     TEXT NOT NULL,
  asset_type      TEXT,
  asset_type_raw  TEXT,
  status          TEXT,
  locality        TEXT,
  province        TEXT,
  start_date      TEXT,
  end_date        TEXT,
  valuation_cents INTEGER,
  appraisal_cents INTEGER,
  min_bid_cents   INTEGER,
  deposit_cents   INTEGER,
  claimed_cents   INTEGER,
  charges_cents   INTEGER,
  cadastral_ref   TEXT,
  description     TEXT,
  main_residence  INTEGER,
  url             TEXT NOT NULL,
  extra           TEXT,
  first_seen      TEXT NOT NULL,
  last_seen       TEXT NOT NULL,
  active          INTEGER NOT NULL DEFAULT 1,
  UNIQUE (source, external_id)
);
CREATE INDEX idx_auctions_filter ON auctions (source, active, asset_type, province);
CREATE INDEX idx_auctions_first_seen ON auctions (first_seen);

CREATE TABLE runs (
  id          INTEGER PRIMARY KEY,
  source      TEXT NOT NULL,
  started_at  TEXT NOT NULL,
  finished_at TEXT,
  status      TEXT NOT NULL,
  items       INTEGER,
  new_items   INTEGER,
  error       TEXT
);

CREATE VIEW auctions_v AS
SELECT a.*,
  100.0 * claimed_cents / NULLIF(valuation_cents, 0) AS pct_claimed_valuation,
  100.0 * claimed_cents / NULLIF(appraisal_cents, 0) AS pct_claimed_appraisal,
  100.0 * deposit_cents / NULLIF(valuation_cents, 0) AS pct_deposit_valuation
FROM auctions a;
