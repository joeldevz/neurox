-- Migration 013: FTS5 index for facts (subject/predicate/object triples)
-- Replaces LIKE '%query%' substring search with tokenized BM25 search.

CREATE VIRTUAL TABLE facts_fts USING fts5(
    id UNINDEXED,
    fact_text,
    content=facts,
    content_rowid=rowid
);

INSERT INTO facts_fts(rowid, id, fact_text)
SELECT rowid, id, subject || ' ' || predicate || ' ' || object
FROM facts;

CREATE TRIGGER trg_facts_ai AFTER INSERT ON facts BEGIN
    INSERT INTO facts_fts(rowid, id, fact_text)
    VALUES (new.rowid, new.id, new.subject || ' ' || new.predicate || ' ' || new.object);
END;

CREATE TRIGGER trg_facts_ad AFTER DELETE ON facts BEGIN
    INSERT INTO facts_fts(facts_fts, rowid, id, fact_text)
    VALUES ('delete', old.rowid, old.id, old.subject || ' ' || old.predicate || ' ' || old.object);
END;

CREATE TRIGGER trg_facts_au AFTER UPDATE ON facts BEGIN
    INSERT INTO facts_fts(facts_fts, rowid, id, fact_text)
    VALUES ('delete', old.rowid, old.id, old.subject || ' ' || old.predicate || ' ' || old.object);
    INSERT INTO facts_fts(rowid, id, fact_text)
    VALUES (new.rowid, new.id, new.subject || ' ' || new.predicate || ' ' || new.object);
END;
