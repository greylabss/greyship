-- name: CreateDeployment :one
INSERT INTO deployments (id, project_id, source, commit_sha, source_key, source_size, source_sha256, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = $1;

-- name: MarkDeploymentQueued :one
UPDATE deployments SET status = 'queued' WHERE id = $1 AND status = 'uploading' RETURNING *;

-- name: ListDeployments :many
SELECT * FROM deployments WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2;

-- name: CreateLocalDeployment :one
INSERT INTO deployments (id, project_id, source, build_mode, commit_sha, created_by)
VALUES ($1, $2, 'cli', 'local', $3, $4)
RETURNING *;

-- name: AddUploadedBinary :exec
INSERT INTO uploaded_binaries (deployment_id, service_name, service_path, service_type, sha256, size_bytes, gz_bytes, object_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListUploadedBinaries :many
SELECT * FROM uploaded_binaries WHERE deployment_id = $1 ORDER BY service_name;

-- name: CreateRedeploy :one
-- A new deployment that runs an existing one's binaries: no upload, no build, straight to release.
INSERT INTO deployments (id, project_id, source, commit_sha, branch, source_key, source_size, source_sha256, build_mode, created_by, rollback_of, status,
                         files_key, files_sha256, files_size)
SELECT sqlc.arg(id), o.project_id, sqlc.arg(source), o.commit_sha, o.branch, o.source_key, o.source_size, o.source_sha256, o.build_mode,
       sqlc.arg(created_by), o.id, 'built', o.files_key, o.files_sha256, o.files_size
FROM deployments o WHERE o.id = sqlc.arg(of)
RETURNING *;

-- name: CopyArtifacts :exec
INSERT INTO artifacts (deployment_id, service_name, service_path, service_type, object_key, sha256, size_bytes, go_version, goarch)
SELECT sqlc.arg(to_deployment)::uuid, a.service_name, a.service_path, a.service_type, a.object_key, a.sha256, a.size_bytes, a.go_version, a.goarch
FROM artifacts a WHERE a.deployment_id = sqlc.arg(from_deployment)::uuid;

-- name: SetDeploymentFiles :exec
UPDATE deployments SET files_key = $2, files_sha256 = $3, files_size = $4 WHERE id = $1;
