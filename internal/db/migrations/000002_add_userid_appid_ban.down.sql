BEGIN;

DROP INDEX IF EXISTS files_ban_comment_idx;
DROP INDEX IF EXISTS files_app_id_idx;
DROP INDEX IF EXISTS files_user_id_idx;

ALTER TABLE file_metas
DROP COLUMN IF EXISTS ban_comment;

ALTER TABLE file_metas
DROP COLUMN IF EXISTS app_id;

ALTER TABLE file_metas
DROP COLUMN IF EXISTS user_id;

COMMIT;
