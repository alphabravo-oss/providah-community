-- +goose Up
DROP FUNCTION filtered_inventory(text,text,text,text,text,text,text,boolean,text);
-- +goose StatementBegin
CREATE FUNCTION filtered_inventory(p_org_id text,p_search text,p_provider text,p_kind text,p_connection_id text,p_tag_key text,p_tag_value text,p_tag_exists boolean,p_tag_name text,p_tag_conditions jsonb,p_tag_match_any boolean) RETURNS SETOF resources LANGUAGE sql STABLE AS $$
SELECT * FROM resources WHERE
coalesce((SELECT CASE WHEN p_tag_match_any THEN bool_or(matched) ELSE bool_and(matched) END FROM (
 SELECT coalesce(CASE WHEN coalesce(c->>'name','')<>'' THEN tag_metadata->'names' ? (c->>'name') ELSE
 tag_metadata->'labels' ? (c->>'key') AND (coalesce((c->>'exists')::boolean,false) OR tag_metadata->'labels'->>(c->>'key')=coalesce(c->>'value','')) END,false) AS matched
 FROM jsonb_array_elements(coalesce(p_tag_conditions,'[]'::jsonb)) c
) conditions),true) AND (p_tag_key::text='' OR (tag_metadata->'labels' ? p_tag_key AND (p_tag_exists::boolean OR tag_metadata->'labels'->>p_tag_key=p_tag_value::text))) AND (p_tag_name::text='' OR tag_metadata->'names' ? p_tag_name) AND org_id=p_org_id AND deleted_at IS NULL AND (p_connection_id::text='' OR connection_id=p_connection_id) AND (p_kind::text='' OR kind=p_kind) AND (p_provider::text='' OR provider=p_provider) AND (p_search::text='' OR strpos(lower(name),lower(p_search))>0 OR strpos(lower(native_id),lower(p_search))>0);
$$;
-- +goose StatementEnd
-- +goose Down
DROP FUNCTION filtered_inventory(text,text,text,text,text,text,text,boolean,text,jsonb,boolean);
-- +goose StatementBegin
CREATE FUNCTION filtered_inventory(p_org_id text,p_search text,p_provider text,p_kind text,p_connection_id text,p_tag_key text,p_tag_value text,p_tag_exists boolean,p_tag_name text) RETURNS SETOF resources LANGUAGE sql STABLE AS $$
SELECT * FROM resources WHERE (p_tag_key::text='' OR (tag_metadata->'labels' ? p_tag_key AND (p_tag_exists::boolean OR tag_metadata->'labels'->>p_tag_key=p_tag_value::text))) AND (p_tag_name::text='' OR tag_metadata->'names' ? p_tag_name) AND org_id=p_org_id AND deleted_at IS NULL AND (p_connection_id::text='' OR connection_id=p_connection_id) AND (p_kind::text='' OR kind=p_kind) AND (p_provider::text='' OR provider=p_provider) AND (p_search::text='' OR strpos(lower(name),lower(p_search))>0 OR strpos(lower(native_id),lower(p_search))>0);
$$;
-- +goose StatementEnd
