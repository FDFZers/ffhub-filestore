BEGIN;

-- ============================================================
-- 文件表
-- ============================================================
CREATE TABLE IF NOT EXISTS file_metas
(
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug         TEXT UNIQUE,
    sha512       TEXT        NOT NULL,
    filename     TEXT        NOT NULL DEFAULT '',
    content_type TEXT        NOT NULL DEFAULT 'application/octet-stream',
    private   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS files_slug_key
    ON file_metas (slug)
    WHERE slug IS NOT NULL;

CREATE INDEX IF NOT EXISTS files_sha512_idx
    ON file_metas (sha512);

CREATE INDEX IF NOT EXISTS files_created_at_idx
    ON file_metas (created_at DESC);

COMMIT;
