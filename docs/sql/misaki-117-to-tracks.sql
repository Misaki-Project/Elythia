-- 対象: 旧 Misaki-develop 29715b8954ea844ff2799d50a8c3903406ca7c6a の
-- migration を 117 まで全て適用済みの DB。実行前の全履歴確認は
-- docs/misaki-migration-reconciliation.md に従う。
-- アプリ・worker・migrate を止め、バックアップから作った使い捨て DB で先に試す。
-- 1 つの DO 文なので、検査・2 つの台帳の変更は全て同じ transaction に入る。
DO $$
DECLARE
    expected_index text;
BEGIN
    IF to_regclass(format('%I.%I', current_schema(), 'schema_migrations')) IS NULL THEN
        RAISE EXCEPTION 'core migration ledger is missing from the current schema';
    END IF;
    LOCK TABLE "schema_migrations" IN ACCESS EXCLUSIVE MODE;
    IF (SELECT count(*) FROM "schema_migrations") <> 1
       OR NOT EXISTS (SELECT 1 FROM "schema_migrations" WHERE version = 117 AND NOT dirty) THEN
        RAISE EXCEPTION 'expected exactly one clean legacy migration version 117';
    END IF;
    IF to_regclass(format('%I.%I', current_schema(), 'schema_migrations_local')) IS NOT NULL THEN
        RAISE EXCEPTION 'local migration ledger already exists; inspect it before reconciliation';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = 'user'
                 AND column_name = 'accountCreatedAt') THEN
        RAISE EXCEPTION 'core migration 117 is already present; this is not the legacy boundary';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = current_schema() AND table_name = 'meta'
                     AND column_name = 'registrationClosed' AND data_type = 'boolean'
                     AND is_nullable = 'NO') THEN
        RAISE EXCEPTION 'legacy migration 106 / core migration 98 has not been verified';
    END IF;
    FOREACH expected_index IN ARRAY ARRAY[
        'IDX_be623adaa4c566baf5d29ce0c8', 'IDX_user_avatarUrl', 'IDX_user_bannerUrl',
        'IDX_drive_file_src', 'IDX_emoji_originalUrl', 'IDX_emoji_publicUrl',
        'IDX_instance_iconUrl', 'IDX_instance_faviconUrl'
    ] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
                       WHERE c.relnamespace = to_regnamespace(current_schema())
                         AND c.relname = expected_index AND i.indisvalid AND i.indisready) THEN
            RAISE EXCEPTION 'missing or invalid legacy index: %', expected_index;
        END IF;
    END LOOP;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = 'avatar_decoration'
                 AND column_name = 'host') THEN
        IF EXISTS (SELECT 1 FROM "avatar_decoration" WHERE "host" IS NOT NULL) THEN
            RAISE EXCEPTION 'remote avatar decorations remain; legacy cleanup is not verified';
        END IF;
    END IF;

    -- オブジェクトの作成・削除・データ修復は行わず、確認済み履歴だけを分ける。
    CREATE TABLE "schema_migrations_local" (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL);
    INSERT INTO "schema_migrations_local" (version, dirty) VALUES (1, false);
    UPDATE "schema_migrations" SET version = 116, dirty = false;
END $$;
