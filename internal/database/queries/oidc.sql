-- name: NewOIDCFlow :exec
INSERT INTO oidc_flows(id,cookie_hash,provider_key,user_id,session_id,mode,return_to) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: ClearExpiredOIDCFlows :exec
DELETE FROM oidc_flows WHERE expires_at<=now();
-- name: ClaimOIDCFlow :one
UPDATE oidc_flows SET claimed=true WHERE id=$1 AND cookie_hash=$2 AND provider_key=$3 AND expires_at>now() AND NOT claimed AND mode IN ('login','link') RETURNING *;
-- name: VerifyOIDCFlow :exec
UPDATE oidc_flows SET mode='verified',user_id=$2,subject=$3 WHERE id=$1 AND mode='login' AND claimed;
-- name: LockOIDCFlow :one
SELECT * FROM oidc_flows WHERE id=$1 AND cookie_hash=$2 AND provider_key=$3 AND mode='verified' AND expires_at>now() FOR UPDATE;
-- name: DeleteOIDCFlow :exec
DELETE FROM oidc_flows WHERE id=$1;
-- name: OIDCLinkedUser :one
SELECT u.* FROM oidc_links l JOIN users u ON u.id=l.user_id WHERE l.issuer=$1 AND l.subject=$2 AND u.active;
-- name: LinkOIDC :exec
INSERT INTO oidc_links(issuer,subject,user_id) VALUES($1,$2,$3);
-- name: UserOIDCLink :one
SELECT * FROM oidc_links WHERE user_id=$1 AND issuer=$2;
-- name: UnlinkOIDC :exec
DELETE FROM oidc_links WHERE user_id=$1 AND issuer=$2;
-- name: GetVerifiedOIDCFlow :one
SELECT * FROM oidc_flows WHERE id=$1 AND cookie_hash=$2 AND provider_key=$3 AND mode='verified' AND expires_at>now();
