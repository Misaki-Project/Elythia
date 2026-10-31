-- CherryPick は連合先から取得したアバターデコレーションも
-- avatar_decoration に保存し、"host" が NULL でない行として区別する。
-- mk-go / upstream Misskey はローカルのカタログだけを扱うため、CherryPick DB
-- からの移行時にリモート由来の行を残さない。
--
-- fresh mk-go / upstream Misskey のテーブルには "host" 列自体が無い。その形でも
-- migration を通せるよう、列が存在するときだけ動的 SQL を実行する。
DO $$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'avatar_decoration'
		  AND column_name = 'host'
	) THEN
		DELETE FROM "avatar_decoration" WHERE "host" IS NOT NULL;
	END IF;
END $$;
