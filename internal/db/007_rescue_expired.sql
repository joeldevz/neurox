UPDATE observations
SET staleness = 'fresh',
    valid_until = NULL,
    invalidated_by = NULL,
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND staleness = 'expired'
  AND id NOT IN (
      SELECT target_id FROM observation_links
      WHERE relation_type = 'supersedes'
  );
