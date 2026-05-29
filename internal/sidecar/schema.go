package sidecar

// Schema is the sidecar's table set. The sidecar is a DERIVED projection:
// it can be deleted and rebuilt from the WAL + native frontmatter (see
// Reproject). effectiveness defaults to the neutral Bayesian prior
// (0+1)/(0+0+2)=0.5 so a brand-new fact is never zeroed out of ranking.
const Schema = `
CREATE TABLE IF NOT EXISTS memory (
  slug          TEXT PRIMARY KEY,
  content_sha   TEXT,
  type          TEXT,
  name          TEXT,
  description   TEXT,
  project       TEXT,
  domains       TEXT,
  created       TEXT,
  last_injected TEXT,
  ref_count     INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'active',
  effectiveness REAL NOT NULL DEFAULT 0.5
);
CREATE TABLE IF NOT EXISTS keyword (slug TEXT, term TEXT, weight REAL);
CREATE TABLE IF NOT EXISTS outcome (slug TEXT, ts TEXT, kind TEXT);
CREATE TABLE IF NOT EXISTS meta    (k TEXT PRIMARY KEY, v TEXT);
CREATE INDEX IF NOT EXISTS idx_keyword_term ON keyword(term);
CREATE INDEX IF NOT EXISTS idx_outcome_slug ON outcome(slug);
`
