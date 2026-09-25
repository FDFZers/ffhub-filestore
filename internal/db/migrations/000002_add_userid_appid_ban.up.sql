BEGIN;

ALTER TABLE file_metas
    ADD COLUMN IF NOT EXISTS user_id BIGINT;
ALTER TABLE file_metas
    ADD COLUMN IF NOT EXISTS app_id BIGINT;
ALTER TABLE file_metas
    ADD COLUMN IF NOT EXISTS ban_comment TEXT;

CREATE INDEX IF NOT EXISTS files_user_id_idx
    ON file_metas (user_id)
    WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS files_app_id_idx
    ON file_metas (app_id)
    WHERE app_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS files_ban_comment_idx
    ON file_metas (ban_comment)
    WHERE ban_comment IS NOT NULL;

COMMIT;
