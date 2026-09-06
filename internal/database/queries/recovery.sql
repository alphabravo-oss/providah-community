-- name: LockRecoveryUser :one
SELECT * FROM users WHERE id=$1 AND active FOR UPDATE;
-- name: ClearRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id=$1;
-- name: AddRecoveryCode :exec
INSERT INTO recovery_codes(user_id,verifier) VALUES($1,$2);
-- name: ConsumeRecoveryCode :execrows
DELETE FROM recovery_codes WHERE user_id=$1 AND verifier=$2;
-- name: RecoveryCodeCount :one
SELECT count(*) FROM recovery_codes WHERE user_id=$1;
-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id=$1;
-- name: RestrictRecoverySession :exec
UPDATE sessions SET mfa_at='epoch' WHERE id=$1;
-- name: AddAccountEvent :exec
INSERT INTO account_events(user_id,action) VALUES($1,$2);
-- name: ListAccountEvents :many
SELECT id,action,occurred_at FROM account_events WHERE user_id=$1 ORDER BY id DESC LIMIT 25;

-- name: ChangePassword :exec
UPDATE users SET password_hash=$2 WHERE id=$1;

-- name: ListAccountSessions :many
SELECT id,expires_at,mfa_at,oidc_id FROM sessions WHERE user_id=$1 AND expires_at>now() AND id<$2 ORDER BY id DESC LIMIT 101;
-- name: DeleteAccountSession :execrows
DELETE FROM sessions WHERE user_id=$1 AND id=$2;

-- name: SeedGlobalAdmin :execrows
UPDATE users SET global_admin=true,mfa_enabled=false,admin_seeded_at=now() WHERE email=$1 AND active AND admin_seeded_at IS NULL;
-- name: SetAccountMFA :exec
UPDATE users SET mfa_enabled=$2 WHERE id=$1;
-- name: SetDisabledMFASeed :exec
UPDATE users SET totp_ciphertext=$2,last_totp_step=0 WHERE id=$1 AND NOT mfa_enabled;
