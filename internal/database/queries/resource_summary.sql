-- name: ResourceSummary :many
SELECT (CASE sqlc.arg(group_by)::text WHEN 'provider' THEN provider WHEN 'kind' THEN kind ELSE status END)::text AS label,
count(*)::bigint AS total, min(observed_at)::timestamptz AS oldest_observation
FROM filtered_inventory(sqlc.arg(org_id)::text,sqlc.arg(search)::text,sqlc.arg(provider)::text,sqlc.arg(kind)::text,sqlc.arg(connection_id)::text,sqlc.arg(tag_key)::text,sqlc.arg(tag_value)::text,sqlc.arg(tag_exists)::boolean,sqlc.arg(tag_name)::text,sqlc.arg(tag_conditions)::jsonb,sqlc.arg(tag_match_any)::boolean,sqlc.arg(filter_region)::text,sqlc.arg(filter_status)::text) GROUP BY 1 ORDER BY 1 LIMIT 1001;
