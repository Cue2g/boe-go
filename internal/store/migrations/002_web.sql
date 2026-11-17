CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
  active        INTEGER NOT NULL DEFAULT 1,
  created_at    TEXT NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);

CREATE TABLE jobs (
  name       TEXT PRIMARY KEY,
  schedule   TEXT NOT NULL DEFAULT '',
  enabled    INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT
);
INSERT INTO jobs (name, schedule, enabled) VALUES
  ('boe', '9 5 * * *', 0),
  ('procuradores', '9 5 * * *', 0),
  ('ss_regulares', '9 5 * * *', 0),
  ('ss_adjudicacion', '9 5 * * *', 0);

CREATE TABLE filters (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  criteria      TEXT NOT NULL DEFAULT '{}',
  alert_mode    TEXT NOT NULL DEFAULT 'none' CHECK (alert_mode IN ('none', 'on_run', 'daily')),
  alert_time    TEXT,
  alert_email   TEXT,
  last_alert_at TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
CREATE INDEX idx_filters_user ON filters (user_id);
CREATE INDEX idx_filters_alert ON filters (alert_mode);

CREATE TABLE alerts_sent (
  id        INTEGER PRIMARY KEY,
  filter_id INTEGER NOT NULL REFERENCES filters (id) ON DELETE CASCADE,
  sent_at   TEXT NOT NULL,
  items     INTEGER NOT NULL,
  error     TEXT
);
CREATE INDEX idx_alerts_sent_filter ON alerts_sent (filter_id, sent_at);

ALTER TABLE runs ADD COLUMN triggered_by TEXT;
ALTER TABLE runs ADD COLUMN user_id INTEGER;
CREATE INDEX idx_runs_source ON runs (source, id);
