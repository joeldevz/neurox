-- Migration 011: Reconcile and recalibrate existing observation scores
-- Purpose: Recalibrate observations with artificially depressed importance values (0.01)
-- that resulted from previous decay logic, while preserving IDs, history, and relationships.
-- 
-- Strategy:
-- 1. Durable observations in Core/Working with importance <= 0.01 get recalibrated
-- 2. Operational observations are NOT inflated (stay as-is)
-- 3. Recalibration is based on: layer, retention, type, access_count, and age
-- 4. activation_level and consolidation_strength are also adjusted for consistency

-- Step 1: Recalibrate durable Core observations with depressed importance
-- These are valuable memories that decayed too aggressively
UPDATE observations SET
    importance = CASE observation_type
        WHEN 'decision' THEN MAX(0.70, importance)
        WHEN 'bugfix' THEN MAX(0.70, importance)
        WHEN 'pattern' THEN MAX(0.65, importance)
        WHEN 'gotcha' THEN MAX(0.65, importance)
        WHEN 'preference' THEN MAX(0.60, importance)
        WHEN 'config' THEN MAX(0.55, importance)
        WHEN 'discovery' THEN MAX(0.50, importance)
        WHEN 'question' THEN MAX(0.40, importance)
        ELSE MAX(0.50, importance)
    END,
    activation_level = CASE 
        WHEN activation_level < 0.30 THEN 0.40
        ELSE activation_level
    END,
    consolidation_strength = CASE 
        WHEN consolidation_strength < 0.50 THEN MAX(consolidation_strength, 0.60)
        ELSE consolidation_strength
    END,
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND layer = 2
  AND retention = 'durable'
  AND importance <= 0.05;

-- Step 2: Recalibrate durable Working observations with depressed importance
-- These haven't reached Core yet but have potential
UPDATE observations SET
    importance = CASE observation_type
        WHEN 'decision' THEN MAX(0.60, importance)
        WHEN 'bugfix' THEN MAX(0.60, importance)
        WHEN 'pattern' THEN MAX(0.55, importance)
        WHEN 'gotcha' THEN MAX(0.55, importance)
        WHEN 'preference' THEN MAX(0.50, importance)
        WHEN 'config' THEN MAX(0.45, importance)
        WHEN 'discovery' THEN MAX(0.40, importance)
        WHEN 'question' THEN MAX(0.35, importance)
        ELSE MAX(0.40, importance)
    END,
    activation_level = CASE 
        WHEN activation_level < 0.25 THEN 0.35
        ELSE activation_level
    END,
    consolidation_strength = CASE 
        WHEN consolidation_strength < 0.30 THEN MAX(consolidation_strength, 0.40)
        ELSE consolidation_strength
    END,
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND layer = 1
  AND retention = 'durable'
  AND importance <= 0.05;

-- Step 3: Recalibrate durable Buffer observations with depressed importance
-- Give them a fighting chance to be promoted
UPDATE observations SET
    importance = CASE observation_type
        WHEN 'decision' THEN MAX(0.50, importance)
        WHEN 'bugfix' THEN MAX(0.50, importance)
        WHEN 'pattern' THEN MAX(0.45, importance)
        WHEN 'gotcha' THEN MAX(0.45, importance)
        WHEN 'preference' THEN MAX(0.40, importance)
        WHEN 'config' THEN MAX(0.35, importance)
        WHEN 'discovery' THEN MAX(0.30, importance)
        WHEN 'question' THEN MAX(0.25, importance)
        ELSE MAX(0.30, importance)
    END,
    activation_level = CASE 
        WHEN activation_level < 0.20 THEN 0.30
        ELSE activation_level
    END,
    consolidation_strength = CASE 
        WHEN consolidation_strength < 0.15 THEN MAX(consolidation_strength, 0.20)
        ELSE consolidation_strength
    END,
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND layer = 0
  AND retention = 'durable'
  AND importance <= 0.05;

-- Step 4: Ensure activation_level and consolidation_strength are reasonable
-- for observations that have good importance but low signals
-- Only for durable observations in Core/Working
UPDATE observations SET
    activation_level = CASE 
        WHEN importance >= 0.70 AND activation_level < 0.30 THEN 0.40
        WHEN importance >= 0.50 AND activation_level < 0.20 THEN 0.30
        ELSE activation_level
    END,
    consolidation_strength = CASE 
        WHEN layer = 2 AND consolidation_strength < 0.40 THEN 0.50
        WHEN layer = 1 AND consolidation_strength < 0.25 THEN 0.35
        ELSE consolidation_strength
    END,
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND layer >= 1
  AND retention = 'durable'
  AND importance >= 0.40;

-- Step 5: Boost consolidation_strength for high-access observations
-- These have been recalled frequently, indicating value
UPDATE observations SET
    consolidation_strength = MIN(0.90, consolidation_strength + (access_count * 0.02)),
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND retention = 'durable'
  AND access_count >= 5
  AND consolidation_strength < 0.70;

-- Step 6: Ensure all observations have at least minimal activation
-- This prevents completely "dead" observations
UPDATE observations SET
    activation_level = 0.20,
    updated_at = datetime('now')
WHERE deleted_at IS NULL
  AND activation_level < 0.10;

-- Create index on importance for efficient queries after recalibration
CREATE INDEX IF NOT EXISTS idx_obs_importance_layer ON observations(importance DESC, layer) WHERE deleted_at IS NULL;
