-- Store the error message for async analysis failures.
ALTER TABLE documents
  ADD COLUMN IF NOT EXISTS error_message TEXT;
