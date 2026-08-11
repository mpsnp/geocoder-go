package pack

const createBaseSchema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
-- Imports can stage hundreds of millions of rows. Keep temporary tables and
-- index builds on disk so pack generation has bounded memory use.
PRAGMA temp_store = FILE;

CREATE TABLE IF NOT EXISTS metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS records (
    id INTEGER PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    source_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    house_number TEXT NOT NULL DEFAULT '',
    street TEXT NOT NULL DEFAULT '',
    unit TEXT NOT NULL DEFAULT '',
    postcode TEXT NOT NULL DEFAULT '',
    locality TEXT NOT NULL DEFAULT '',
    district TEXT NOT NULL DEFAULT '',
    region TEXT NOT NULL DEFAULT '',
    country_code TEXT NOT NULL DEFAULT '',
    country TEXT NOT NULL DEFAULT '',
    lat REAL NOT NULL,
    lon REAL NOT NULL,
    importance REAL NOT NULL DEFAULT 0,
    aliases TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL,
    search_text TEXT NOT NULL,
    fingerprint TEXT NOT NULL UNIQUE
);

CREATE INDEX IF NOT EXISTS records_country_postcode ON records(country_code, postcode);
CREATE INDEX IF NOT EXISTS records_kind ON records(kind);
`

const createSearchSchema = `
DROP TABLE IF EXISTS records_fts;
DROP TABLE IF EXISTS records_rtree;

CREATE VIRTUAL TABLE records_fts USING fts5(
    search_text,
    content='records',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2',
    prefix='2 3 4'
);
INSERT INTO records_fts(rowid, search_text) SELECT id, search_text FROM records;

CREATE VIRTUAL TABLE records_rtree USING rtree(
    id,
    min_lat, max_lat,
    min_lon, max_lon
);
INSERT INTO records_rtree(id, min_lat, max_lat, min_lon, max_lon)
SELECT id, lat, lat, lon, lon FROM records;
`
