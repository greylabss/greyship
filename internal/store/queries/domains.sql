-- name: AddDomain :exec
INSERT INTO domains (hostname, service_id, created_by) VALUES ($1, $2, $3);

-- name: ListProjectDomains :many
SELECT d.hostname::text AS hostname, d.created_at, s.name AS service_name, s.subdomain::text AS subdomain
FROM domains d JOIN services s ON s.id = d.service_id
WHERE s.project_id = $1 ORDER BY d.created_at;

-- name: DeleteProjectDomain :execrows
DELETE FROM domains d USING services s WHERE d.service_id = s.id AND s.project_id = $1 AND d.hostname = $2;

-- name: ProjectWebServices :many
-- The services a hostname can point at: the project's public web services.
SELECT * FROM services WHERE project_id = $1 AND type = 'web' AND subdomain IS NOT NULL ORDER BY name;

-- name: RoutingDomains :many
-- Custom hostnames the routers serve, each as the subdomain label of its service.
SELECT d.hostname::text AS hostname, s.subdomain::text AS subdomain
FROM domains d
JOIN services s ON s.id = d.service_id AND s.type = 'web' AND s.subdomain IS NOT NULL
JOIN projects p ON p.id = s.project_id AND p.deleting_at IS NULL;

-- name: DomainTaken :one
SELECT EXISTS (SELECT 1 FROM domains WHERE hostname = $1);
