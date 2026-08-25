-- Performance indexes added when jobs/images tables grew past ~500K rows.
-- /jobs page used to do full-table SCAN + TEMP B-TREE ORDER BY because no index
-- backed status / (status, finished_at) lookups.

-- GROUP BY status (used by /jobs stats card) and per-status filter pattern.
CREATE INDEX IF NOT EXISTS idx_jobs_status_id ON jobs(status, id DESC);

-- "in last 60s" rate queries: WHERE status=? AND finished_at > ?
CREATE INDEX IF NOT EXISTS idx_jobs_status_finished
  ON jobs(status, finished_at DESC)
  WHERE finished_at IS NOT NULL;
