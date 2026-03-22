ALTER TABLE observations ADD COLUMN retention TEXT NOT NULL DEFAULT 'durable' CHECK (retention IN ('operational', 'durable'));
