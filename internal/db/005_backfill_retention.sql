-- Backfill retention policy for existing observations.
-- This migration reclassifies known operational patterns and cleans up empty reflections.

-- 1. Consolidator-generated implementation notes → operational.
UPDATE observations
SET retention = 'operational'
WHERE source = 'consolidator' AND deleted_at IS NULL;

-- 2. Known step/plan execution titles → operational.
UPDATE observations
SET retention = 'operational'
WHERE deleted_at IS NULL
  AND (
    title LIKE 'Implement Step%'
    OR title LIKE 'Step %'
    OR title LIKE 'Plan completed%'
    OR title LIKE 'Build flags%'
    OR title LIKE 'File Observations%'
    OR title LIKE 'Embeddings%'
    OR title LIKE 'TestToolsList%'
    OR title LIKE 'Tracker Wiring%'
    OR title LIKE 'Fixing schema%'
    OR title LIKE 'Renamed Local%'
    OR title LIKE 'Named Return%'
    OR title LIKE 'Queue Imported%'
    OR title LIKE 'Embed Mocking%'
    OR title LIKE 'Update NewPipeline%'
    OR title LIKE 'LLM availability%'
  );

-- 3. Soft-delete empty or trivially short reflections.
UPDATE observations
SET deleted_at = datetime('now'), updated_at = datetime('now')
WHERE source = 'reflection'
  AND (content = '' OR LENGTH(TRIM(content)) < 50)
  AND deleted_at IS NULL;
