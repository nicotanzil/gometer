-- Legacy configurations remain private. Only explicitly classified documents
-- can be returned by the team authoring and export APIs.
ALTER TABLE gometer.test_definitions ADD COLUMN shared_document jsonb
 CHECK (shared_document IS NULL OR jsonb_typeof(shared_document)='object');
