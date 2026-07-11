-- Migration 0001: Authentication, Roles, Sessions, and Invites

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(32) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash VARCHAR(64) PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    revoked_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS invites (
    token_hash VARCHAR(64) PRIMARY KEY,
    creator_id UUID REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    used_at TIMESTAMP WITH TIME ZONE,
    used_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS roles (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(50) NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS permissions (
    id VARCHAR(50) PRIMARY KEY,
    description TEXT
);

CREATE TABLE IF NOT EXISTS role_permissions (
    role_id VARCHAR(50) REFERENCES roles(id) ON DELETE CASCADE,
    permission_id VARCHAR(50) REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    role_id VARCHAR(50) REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- Seed default roles
INSERT INTO roles (id, name) VALUES 
('Owner', 'Owner'),
('Administrator', 'Administrator'),
('Moderator', 'Moderator'),
('Member', 'Member'),
('Contributor', 'Contributor'),
('Librarian', 'Librarian')
ON CONFLICT (id) DO NOTHING;

-- Seed default permissions
INSERT INTO permissions (id, description) VALUES
('manage_community', 'Manage community settings and global options'),
('manage_members', 'Disable/enable members and reset user passwords'),
('manage_roles', 'Create, edit, and assign roles'),
('manage_channels', 'Manage channels and role permission overrides'),
('view_channel', 'View channel details and subscribe to chat history/events'),
('send_messages', 'Send chat messages in channels'),
('moderate_chat', 'Delete or moderate other users'' chat messages'),
('join_voice', 'Join voice channels and mint LiveKit voice tokens'),
('view_media', 'Access and play Jellyfin media streaming proxy'),
('view_library', 'Access Grimmory e-book catalog proxy'),
('view_files', 'View and download files from the shared library'),
('upload_files', 'Upload files to the shared library'),
('upload_books', 'Upload books to the Grimmory shared bookdrop'),
('manage_files', 'Delete and audit files in the shared library')
ON CONFLICT (id) DO NOTHING;

-- Map permissions to roles
-- Owner and Administrator get all permissions
INSERT INTO role_permissions (role_id, permission_id)
SELECT 'Owner', id FROM permissions
ON CONFLICT (role_id, permission_id) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT 'Administrator', id FROM permissions
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Moderator permissions
INSERT INTO role_permissions (role_id, permission_id) VALUES
('Moderator', 'view_channel'),
('Moderator', 'send_messages'),
('Moderator', 'moderate_chat'),
('Moderator', 'join_voice'),
('Moderator', 'view_media'),
('Moderator', 'view_library'),
('Moderator', 'view_files')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Member permissions
INSERT INTO role_permissions (role_id, permission_id) VALUES
('Member', 'view_channel'),
('Member', 'send_messages'),
('Member', 'join_voice'),
('Member', 'view_media'),
('Member', 'view_library'),
('Member', 'view_files'),
('Member', 'upload_files')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Contributor permissions
INSERT INTO role_permissions (role_id, permission_id) VALUES
('Contributor', 'view_channel'),
('Contributor', 'send_messages'),
('Contributor', 'join_voice'),
('Contributor', 'view_media'),
('Contributor', 'view_library'),
('Contributor', 'view_files'),
('Contributor', 'upload_files'),
('Contributor', 'upload_books')
ON CONFLICT (role_id, permission_id) DO NOTHING;

-- Librarian permissions
INSERT INTO role_permissions (role_id, permission_id) VALUES
('Librarian', 'view_channel'),
('Librarian', 'send_messages'),
('Librarian', 'join_voice'),
('Librarian', 'view_media'),
('Librarian', 'view_library'),
('Librarian', 'view_files'),
('Librarian', 'upload_files'),
('Librarian', 'upload_books'),
('Librarian', 'manage_files')
ON CONFLICT (role_id, permission_id) DO NOTHING;
