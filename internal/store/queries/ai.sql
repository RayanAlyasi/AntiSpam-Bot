-- name: AIBudgetLock :exec
-- Serializes AI budget reservations instance-wide (budget.Store requires one serial order).
SELECT pg_advisory_xact_lock(hashtext('app.ai_usage'));

-- name: AIUsageCounts :one
SELECT
    COALESCE(SUM(calls) FILTER (WHERE day = sqlc.arg(day)::date AND group_jid = sqlc.arg(group_jid)::text), 0)::int AS group_day,
    COALESCE(SUM(calls) FILTER (WHERE day = sqlc.arg(day)::date), 0)::int AS instance_day,
    COALESCE(SUM(calls), 0)::int AS instance_month
FROM app.ai_usage
WHERE day >= date_trunc('month', sqlc.arg(day)::date)::date
  AND day < (date_trunc('month', sqlc.arg(day)::date) + interval '1 month')::date;

-- name: AIUsageAddCall :exec
INSERT INTO app.ai_usage (day, group_jid, calls) VALUES (sqlc.arg(day)::date, sqlc.arg(group_jid), 1)
ON CONFLICT (day, group_jid) DO UPDATE SET calls = app.ai_usage.calls + 1;

-- name: AIUsageAddTokens :exec
INSERT INTO app.ai_usage (day, group_jid, input_tokens, output_tokens, cost_micros)
VALUES (sqlc.arg(day)::date, sqlc.arg(group_jid), sqlc.arg(input_tokens), sqlc.arg(output_tokens), sqlc.arg(cost_micros))
ON CONFLICT (day, group_jid) DO UPDATE SET
    input_tokens  = app.ai_usage.input_tokens + EXCLUDED.input_tokens,
    output_tokens = app.ai_usage.output_tokens + EXCLUDED.output_tokens,
    cost_micros   = app.ai_usage.cost_micros + EXCLUDED.cost_micros;

-- name: AIUsageRow :one
SELECT day, group_jid, calls, input_tokens, output_tokens, cost_micros
FROM app.ai_usage WHERE day = sqlc.arg(day)::date AND group_jid = sqlc.arg(group_jid);

-- name: AICacheGet :one
SELECT key, model, prompt_version, result, created_at
FROM app.ai_cache WHERE key = sqlc.arg(key) AND created_at >= sqlc.arg(not_before);

-- name: AICachePut :exec
INSERT INTO app.ai_cache (key, model, prompt_version, result, created_at)
VALUES (sqlc.arg(key), sqlc.arg(model), sqlc.arg(prompt_version), sqlc.arg(result), sqlc.arg(created_at))
ON CONFLICT (key) DO UPDATE SET
    model = EXCLUDED.model, prompt_version = EXCLUDED.prompt_version,
    result = EXCLUDED.result, created_at = EXCLUDED.created_at;

-- name: AICachePurge :execrows
DELETE FROM app.ai_cache WHERE created_at < sqlc.arg(before);
