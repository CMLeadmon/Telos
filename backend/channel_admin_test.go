package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func channelAdminFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	f := withFixture(t)
	oldDB, oldRedis := dbPool, redisClient
	dbPool = f.DB
	redisClient = f.Redis
	t.Cleanup(func() { dbPool, redisClient = oldDB, oldRedis })
	var uid string
	f.DB.QueryRow(context.Background(), `INSERT INTO users (username,password_hash) VALUES ('chanadmin','x') RETURNING id::text`).Scan(&uid)
	return f.DB, uid
}

func TestChannelAdminCRUD(t *testing.T) {
	db, actor := channelAdminFixture(t)
	ctx := context.Background()

	// Slug is lowercased; type validated.
	id, err := CreateChannel(ctx, actor, "General-Chat", "text")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var name, typ string
	db.QueryRow(ctx, `SELECT name, type FROM channels WHERE id=$1`, id).Scan(&name, &typ)
	if name != "general-chat" {
		t.Fatalf("slug not lowercased: %q", name)
	}

	// Invalid slug and type are rejected.
	if _, err := CreateChannel(ctx, actor, "Bad Name!", "text"); err != errChannelSlugInvalid {
		t.Fatalf("invalid slug = %v", err)
	}
	if _, err := CreateChannel(ctx, actor, "ok-name", "hologram"); err != errChannelTypeInvalid {
		t.Fatalf("invalid type = %v", err)
	}
	// Duplicate name conflicts.
	if _, err := CreateChannel(ctx, actor, "general-chat", "text"); err != errChannelNameTaken {
		t.Fatalf("duplicate = %v, want name-taken", err)
	}

	if err := UpdateChannel(ctx, actor, id, "renamed-chan"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := DeleteChannel(ctx, actor, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// An audit trail was recorded.
	var n int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM channel_audit WHERE channel_id=$1`, id).Scan(&n)
	if n < 3 {
		t.Fatalf("expected create/update/delete audit rows, got %d", n)
	}
}

func TestChannelOverrideDenyPrecedence(t *testing.T) {
	db, actor := channelAdminFixture(t)
	ctx := context.Background()
	id, _ := CreateChannel(ctx, actor, "override-chan", "text")

	// A role that is globally granted send_messages.
	db.Exec(ctx, `INSERT INTO roles (id,name) VALUES ('TR','TR') ON CONFLICT DO NOTHING`)
	db.Exec(ctx, `INSERT INTO role_permissions (role_id,permission_id) VALUES ('TR','send_messages') ON CONFLICT DO NOTHING`)
	member := &UserContext{ID: actor, Roles: []string{"TR"}}

	// Inherit: the global grant applies.
	if ok, _ := hasPermission(ctx, member, "send_messages", &id); !ok {
		t.Fatal("global grant should apply with no override")
	}
	// Explicit deny wins over the global grant.
	if err := SetRoleOverride(ctx, actor, id, "TR", "send_messages", DecisionDeny); err != nil {
		t.Fatalf("set deny: %v", err)
	}
	if ok, _ := hasPermission(ctx, member, "send_messages", &id); ok {
		t.Fatal("explicit deny must override the global grant")
	}
	// Explicit allow restores access.
	if err := SetRoleOverride(ctx, actor, id, "TR", "send_messages", DecisionAllow); err != nil {
		t.Fatalf("set allow: %v", err)
	}
	if ok, _ := hasPermission(ctx, member, "send_messages", &id); !ok {
		t.Fatal("explicit allow should grant access")
	}
	// Inherit clears the override.
	if err := SetRoleOverride(ctx, actor, id, "TR", "send_messages", DecisionInherit); err != nil {
		t.Fatalf("set inherit: %v", err)
	}
	var rows int
	db.QueryRow(ctx, `SELECT COUNT(*) FROM channel_permission_overrides WHERE channel_id=$1`, id).Scan(&rows)
	if rows != 0 {
		t.Fatalf("inherit should remove the row, got %d", rows)
	}

	// Deny applies to a non-Owner Administrator too; Owner bypasses.
	SetRoleOverride(ctx, actor, id, "Administrator", "send_messages", DecisionDeny)
	db.Exec(ctx, `INSERT INTO role_permissions (role_id,permission_id) VALUES ('Administrator','send_messages') ON CONFLICT DO NOTHING`)
	admin := &UserContext{ID: actor, Roles: []string{"Administrator"}}
	if ok, _ := hasPermission(ctx, admin, "send_messages", &id); ok {
		t.Fatal("deny must apply to Administrator")
	}
	owner := &UserContext{ID: actor, Roles: []string{"Owner"}}
	if ok, _ := hasPermission(ctx, owner, "send_messages", &id); !ok {
		t.Fatal("Owner must bypass channel overrides")
	}
}
