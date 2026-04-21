-- name: CreateInvitation :exec
INSERT INTO invitations (id, email, role, invited_by)
VALUES (?, ?, ?, ?);

-- name: GetInvitationByEmail :one
SELECT id, email, role, invited_by, created_at
FROM invitations
WHERE email = ?;

-- name: GetInvitationByID :one
SELECT id, email, role, invited_by, created_at
FROM invitations
WHERE id = ?;

-- name: ListInvitations :many
SELECT id, email, role, invited_by, created_at
FROM invitations
ORDER BY created_at;

-- name: DeleteInvitation :exec
DELETE FROM invitations WHERE id = ?;

-- name: DeleteInvitationByEmail :exec
DELETE FROM invitations WHERE email = ?;
