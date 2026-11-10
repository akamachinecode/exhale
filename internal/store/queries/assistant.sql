-- name: InsertAssistantUsage :exec
INSERT INTO assistant_usage (id, household_id, member_id, at, kind, surface, provider, model, rounds, input_tokens, output_tokens, cost_micros, failed)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAssistantUsage :many
SELECT u.*, COALESCE(m.name, '') AS member_name
FROM assistant_usage u LEFT JOIN members m ON m.id = u.member_id
WHERE u.household_id = sqlc.arg(household_id) AND (sqlc.arg(member_id) = '' OR u.member_id = sqlc.arg(member_id))
ORDER BY u.at DESC, u.id DESC
LIMIT sqlc.arg(lim);

-- name: SumAssistantUsage :one
SELECT
    COUNT(*) AS questions,
    CAST(COALESCE(SUM(input_tokens), 0) AS INTEGER) AS input_tokens,
    CAST(COALESCE(SUM(output_tokens), 0) AS INTEGER) AS output_tokens,
    CAST(COALESCE(SUM(cost_micros), 0) AS INTEGER) AS cost_micros,
    CAST(COALESCE(SUM(cost_micros IS NULL), 0) AS INTEGER) AS unpriced
FROM assistant_usage
WHERE household_id = sqlc.arg(household_id) AND at >= sqlc.arg(since) AND at < sqlc.arg(until);

-- name: DeleteAssistantUsageBefore :exec
DELETE FROM assistant_usage WHERE at < ?;
