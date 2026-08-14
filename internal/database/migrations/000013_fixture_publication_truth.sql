-- A fixture account performs only a deterministic local simulation. Historical
-- public-source tasks that used one must not remain marked as remotely
-- published. Keep their attempt evidence, but reopen the draft with a blocker.
WITH invalid_publications AS (
    SELECT
        publication.id,
        publication.publish_job_id,
        publication.task_id,
        publication.platform
    FROM platform_publications publication
    JOIN platform_accounts account ON account.id = publication.account_id
    JOIN tasks task ON task.id = publication.task_id
    WHERE account.auth_mode = 'fixture'
      AND lower(task.source_url) !~ '^https?://fixture-provider(:[0-9]+)?(/|$)'
),
repaired_publications AS (
    UPDATE platform_publications publication
    SET
        status = 'failed',
        error_code = 'fixture_account_real_source_forbidden',
        error_message = '该记录仅完成了本地模拟，没有向平台提交；请切换为已校验的 Cookie 认证账号',
        error_retryable = false,
        uncertain_since = NULL,
        locked_at = NULL,
        locked_by = '',
        completed_at = NULL,
        updated_at = now(),
        version = publication.version + 1
    FROM invalid_publications invalid
    WHERE publication.id = invalid.id
    RETURNING
        publication.publish_job_id,
        publication.task_id,
        publication.platform
),
affected_jobs AS (
    SELECT
        publish_job_id,
        jsonb_agg(
            jsonb_build_object(
                'code', 'fixture_account_real_source_forbidden',
                'platform', platform,
                'message', '当前绑定的是本地测试账号，不会向平台投稿；真实来源必须使用 Cookie 认证账号',
                'action', 'bind_real_platform_account'
            )
            ORDER BY platform
        ) AS blockers
    FROM repaired_publications
    GROUP BY publish_job_id
),
repaired_jobs AS (
    UPDATE publish_jobs job
    SET
        status = 'blocked',
        blockers = affected.blockers,
        completed_at = NULL,
        updated_at = now(),
        version = job.version + 1
    FROM affected_jobs affected
    WHERE job.id = affected.publish_job_id
    RETURNING job.task_id
),
repaired_tasks AS (
    UPDATE tasks task
    SET
        status = 'ready_to_publish',
        error_code = '',
        error_message = '',
        error_retryable = false,
        updated_at = now(),
        version = task.version + 1
    WHERE task.id IN (SELECT task_id FROM repaired_jobs)
    RETURNING task.id
)
UPDATE task_steps step
SET
    status = 'failed',
    progress = 100,
    error_code = 'fixture_account_real_source_forbidden',
    error_message = '本地模拟没有向真实平台提交，请在投稿页切换为 Cookie 认证账号',
    finished_at = now(),
    updated_at = now()
WHERE step.task_id IN (SELECT id FROM repaired_tasks)
  AND step.kind = 'publish';
