-- Migration 0007: permission for shared Grimmory catalog management.
-- 0005 is already the reading-progress migration and 0006 is chat enhancements.

INSERT INTO permissions (id, description) VALUES
    ('manage_library', 'Edit metadata, replace covers, and delete shared library books')
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT grants.role_id, 'manage_library'
FROM (
    SELECT 'Librarian' AS role_id
    UNION
    SELECT rp.role_id
    FROM role_permissions rp
    WHERE rp.permission_id = 'manage_community'
) grants
JOIN roles r ON r.id = grants.role_id
ON CONFLICT (role_id, permission_id) DO NOTHING;
