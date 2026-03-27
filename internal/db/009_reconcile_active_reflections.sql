-- Migration 009: Reconcile active reflections
-- Soft-deletes all but the most recent reflection observation per namespace,
-- leaving only one active reflection per namespace in observations.
-- Does NOT delete rows from the reflections table (append-only history).

WITH ranked_reflections AS (
    SELECT 
        id,
        namespace,
        ROW_NUMBER() OVER (
            PARTITION BY namespace 
            ORDER BY created_at DESC, id DESC
        ) as rn
    FROM observations
    WHERE deleted_at IS NULL
      AND source = 'reflection'
)
UPDATE observations
SET deleted_at = datetime('now'),
    updated_at = datetime('now')
WHERE id IN (
    SELECT id 
    FROM ranked_reflections 
    WHERE rn > 1
);
