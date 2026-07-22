package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// wpClock is the injectable clock used for lease/heartbeat timing so expiry is
// deterministically testable.
var wpClock = time.Now

const (
	wpHeartbeatInterval = 5 * time.Second
	wpLeaseWindow       = 15 * time.Second
	wpStateTTL          = 24 * time.Hour
	wpMinRate           = 0.5
	wpMaxRate           = 2.0
)

var (
	errWPNotHost       = errors.New("only the current host may control the party")
	errWPStaleVersion  = errors.New("stale party version")
	errWPBadControl    = errors.New("invalid playback control")
	errWPLeaseExpired  = errors.New("host lease expired")
	errWPNotSuccessor  = errors.New("only the accepted successor may claim the host lease")
	errWPNotFound      = errors.New("watch party not found")
	errWPHeartbeatFast = errors.New("heartbeat too frequent")
)

// WatchParty is the durable party identity.
type WatchParty struct {
	ID             string     `json:"id"`
	MediaItemID    string     `json:"mediaItemId"`
	TextChannelID  string     `json:"textChannelId,omitempty"`
	VoiceChannelID string     `json:"voiceChannelId,omitempty"`
	HostID         string     `json:"hostId"`
	HostGeneration int64      `json:"hostGeneration"`
	CreatedAt      time.Time  `json:"createdAt"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
}

// WatchPartyState is the host-authoritative playback state (in Redis).
type WatchPartyState struct {
	Version         int64     `json:"version"`
	Action          string    `json:"action"` // playing | paused
	PositionSeconds float64   `json:"positionSeconds"`
	PlaybackRate    float64   `json:"playbackRate"`
	MediaItemID     string    `json:"mediaItemId"`
	ServerTime      time.Time `json:"serverTime"`
	LeaseExpiresAt  time.Time `json:"leaseExpiresAt"`
}

// WatchPartyControlInput is a host control request.
type WatchPartyControlInput struct {
	Action          string
	PositionSeconds float64
	PlaybackRate    float64
	MediaItemID     string
	ExpectedVersion int64
}

func wpStateKey(id string) string { return "telos:wp:" + id + ":state" }
func wpLeaseKey(id string) string { return "telos:wp:" + id + ":lease" }
func wpBeatKey(id string) string  { return "telos:wp:" + id + ":beat" }

type hostLease struct {
	HostID     string    `json:"hostId"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// CreateParty creates a durable party (media/channel authorization is the
// caller's responsibility), seeds the host member, the host lease, and a paused
// initial state.
func CreateParty(ctx context.Context, hostID, mediaItemID, textCh, voiceCh string) (WatchParty, error) {
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return WatchParty{}, err
	}
	defer tx.Rollback(ctx)
	var p WatchParty
	err = tx.QueryRow(ctx, `
		INSERT INTO watch_parties (media_item_id, text_channel_id, voice_channel_id, host_id)
		VALUES ($1, NULLIF($2,'')::uuid, NULLIF($3,'')::uuid, $4::uuid)
		RETURNING id::text, media_item_id, COALESCE(text_channel_id::text,''), COALESCE(voice_channel_id::text,''), host_id::text, host_generation, created_at
	`, mediaItemID, textCh, voiceCh, hostID).Scan(&p.ID, &p.MediaItemID, &p.TextChannelID, &p.VoiceChannelID, &p.HostID, &p.HostGeneration, &p.CreatedAt)
	if err != nil {
		return WatchParty{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO watch_party_members (party_id, user_id, state) VALUES ($1,$2::uuid,'joined')`, p.ID, hostID); err != nil {
		return WatchParty{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WatchParty{}, err
	}

	now := wpClock()
	_ = writeLease(ctx, p.ID, hostLease{HostID: hostID, Generation: p.HostGeneration, ExpiresAt: now.Add(wpLeaseWindow)})
	// Creation counts as the host's first heartbeat, so the next one must wait
	// out the heartbeat interval.
	if tb, err := now.MarshalText(); err == nil {
		redisClient.Set(ctx, wpBeatKey(p.ID), tb, wpHeartbeatInterval*2)
	}
	_ = writeState(ctx, p.ID, WatchPartyState{
		Version: 1, Action: "paused", PositionSeconds: 0, PlaybackRate: 1,
		MediaItemID: mediaItemID, ServerTime: now, LeaseExpiresAt: now.Add(wpLeaseWindow),
	})
	return p, nil
}

// GetParty returns durable identity, recovering it after a Redis loss.
func GetParty(ctx context.Context, id string) (WatchParty, error) {
	var p WatchParty
	err := dbPool.QueryRow(ctx, `
		SELECT id::text, media_item_id, COALESCE(text_channel_id::text,''), COALESCE(voice_channel_id::text,''),
		       host_id::text, host_generation, created_at, ended_at
		FROM watch_parties WHERE id = $1
	`, id).Scan(&p.ID, &p.MediaItemID, &p.TextChannelID, &p.VoiceChannelID, &p.HostID, &p.HostGeneration, &p.CreatedAt, &p.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WatchParty{}, errWPNotFound
	}
	return p, err
}

// Invite records a pending invitation and materializes a notification for the
// invitee through the outbox, in one transaction.
func Invite(ctx context.Context, partyID, hostID, inviteeID string) error {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return err
	}
	if p.HostID != hostID {
		return errWPNotHost
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var inviteID string
	err = tx.QueryRow(ctx, `
		INSERT INTO watch_party_invitations (party_id, invitee_id) VALUES ($1,$2::uuid)
		ON CONFLICT (party_id, invitee_id) DO UPDATE SET status='pending'
		RETURNING id::text
	`, partyID, inviteeID).Scan(&inviteID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"partyId": partyID})
	if _, err := CreateNotification(ctx, tx, NotificationInput{
		RecipientID: inviteeID, ActorID: hostID, Kind: NotifyWatchPartyInvite,
		ResourceType: "watch_party", ResourceID: partyID,
		IdempotencyKey: "wpinvite:" + inviteID, Payload: payload,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RespondInvite accepts or declines an invitation; acceptance adds the member.
func RespondInvite(ctx context.Context, partyID, inviteeID string, accept bool) error {
	status := "declined"
	if accept {
		status = "accepted"
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE watch_party_invitations SET status=$3 WHERE party_id=$1 AND invitee_id=$2::uuid AND status='pending'`, partyID, inviteeID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errWPNotFound
	}
	if accept {
		if _, err := tx.Exec(ctx, `INSERT INTO watch_party_members (party_id, user_id, state) VALUES ($1,$2::uuid,'joined') ON CONFLICT (party_id,user_id) DO UPDATE SET state='joined'`, partyID, inviteeID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetMemberState transitions a member (join/leave/detach/rejoin).
func SetMemberState(ctx context.Context, partyID, userID, state string) error {
	if state != "joined" && state != "detached" && state != "left" {
		return errWPBadControl
	}
	tag, err := dbPool.Exec(ctx, `UPDATE watch_party_members SET state=$3 WHERE party_id=$1 AND user_id=$2::uuid`, partyID, userID, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errWPNotFound
	}
	return nil
}

// EndParty marks the party ended and clears its Redis state/lease.
func EndParty(ctx context.Context, partyID, hostID string) error {
	tag, err := dbPool.Exec(ctx, `UPDATE watch_parties SET ended_at = now() WHERE id=$1 AND host_id=$2::uuid AND ended_at IS NULL`, partyID, hostID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errWPNotHost
	}
	redisClient.Del(ctx, wpStateKey(partyID), wpLeaseKey(partyID), wpBeatKey(partyID))
	return nil
}

// ── Host lease ────────────────────────────────────────────────────────────

func writeLease(ctx context.Context, id string, l hostLease) error {
	b, _ := json.Marshal(l)
	return redisClient.Set(ctx, wpLeaseKey(id), b, wpStateTTL).Err()
}

func readLease(ctx context.Context, id string) (hostLease, bool) {
	v, err := redisClient.Get(ctx, wpLeaseKey(id)).Result()
	if err != nil {
		return hostLease{}, false
	}
	var l hostLease
	if json.Unmarshal([]byte(v), &l) != nil {
		return hostLease{}, false
	}
	return l, true
}

// RenewHostLease is the host heartbeat. It rejects a beat more frequent than the
// server bound and a beat from anyone but the current host, and extends the
// lease by the lease window.
func RenewHostLease(ctx context.Context, partyID, hostID string) (time.Time, error) {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return time.Time{}, err
	}
	if p.HostID != hostID {
		return time.Time{}, errWPNotHost
	}
	now := wpClock()
	// Enforce the minimum heartbeat interval using a short marker key.
	beatKey := wpBeatKey(partyID)
	if last, err := redisClient.Get(ctx, beatKey).Result(); err == nil {
		var t time.Time
		if t.UnmarshalText([]byte(last)) == nil && now.Sub(t) < wpHeartbeatInterval-time.Second {
			return time.Time{}, errWPHeartbeatFast
		}
	}
	tb, _ := now.MarshalText()
	redisClient.Set(ctx, beatKey, tb, wpHeartbeatInterval*2)

	expires := now.Add(wpLeaseWindow)
	if err := writeLease(ctx, partyID, hostLease{HostID: hostID, Generation: p.HostGeneration, ExpiresAt: expires}); err != nil {
		return time.Time{}, err
	}
	return expires, nil
}

// LeaseValid reports whether the host lease is currently valid.
func LeaseValid(ctx context.Context, partyID string) bool {
	l, ok := readLease(ctx, partyID)
	return ok && wpClock().Before(l.ExpiresAt)
}

// ── Host succession ───────────────────────────────────────────────────────

// OfferHost records the host's explicit successor offer, bound to the current
// host generation.
func OfferHost(ctx context.Context, partyID, hostID, successorID string) error {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return err
	}
	if p.HostID != hostID {
		return errWPNotHost
	}
	// Cancel any prior offer for this generation, then record the new one.
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE watch_party_host_offers SET status='cancelled' WHERE party_id=$1 AND host_generation=$2 AND status='offered'`, partyID, p.HostGeneration); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO watch_party_host_offers (party_id, host_generation, successor_id) VALUES ($1,$2,$3::uuid)`, partyID, p.HostGeneration, successorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcceptHostOffer marks the successor's explicit consent for the current
// generation.
func AcceptHostOffer(ctx context.Context, partyID, successorID string) error {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return err
	}
	tag, err := dbPool.Exec(ctx, `UPDATE watch_party_host_offers SET status='accepted' WHERE party_id=$1 AND host_generation=$2 AND successor_id=$3::uuid AND status='offered'`, partyID, p.HostGeneration, successorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errWPNotFound
	}
	return nil
}

// CancelHostOffer withdraws the pending offer for the current generation.
func CancelHostOffer(ctx context.Context, partyID, hostID string) error {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return err
	}
	if p.HostID != hostID {
		return errWPNotHost
	}
	_, err = dbPool.Exec(ctx, `UPDATE watch_party_host_offers SET status='cancelled' WHERE party_id=$1 AND host_generation=$2 AND status IN ('offered','accepted')`, partyID, p.HostGeneration)
	return err
}

// acceptedSuccessor returns the successor who accepted the offer for a
// generation, if any.
func acceptedSuccessor(ctx context.Context, partyID string, generation int64) (string, bool) {
	var sid string
	err := dbPool.QueryRow(ctx, `SELECT successor_id::text FROM watch_party_host_offers WHERE party_id=$1 AND host_generation=$2 AND status='accepted' ORDER BY created_at DESC LIMIT 1`, partyID, generation).Scan(&sid)
	if err != nil {
		return "", false
	}
	return sid, true
}

// ClaimHostLease transfers the host role to the claimant, permitted ONLY when
// the lease has expired AND the claimant is the accepted successor for the
// current generation. No election, random choice, or arbitrary claim is ever
// allowed. A completed transfer bumps the host generation, invalidating stale
// offers.
func ClaimHostLease(ctx context.Context, partyID, claimantID string) error {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return err
	}
	if LeaseValid(ctx, partyID) {
		return errWPLeaseExpired // lease still valid: cannot claim
	}
	successor, ok := acceptedSuccessor(ctx, partyID, p.HostGeneration)
	if !ok || successor != claimantID {
		return errWPNotSuccessor
	}
	tx, err := dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var newGen int64
	if err := tx.QueryRow(ctx, `UPDATE watch_parties SET host_id=$2::uuid, host_generation=host_generation+1 WHERE id=$1 RETURNING host_generation`, partyID, claimantID).Scan(&newGen); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO watch_party_members (party_id,user_id,state) VALUES ($1,$2::uuid,'joined') ON CONFLICT (party_id,user_id) DO UPDATE SET state='joined'`, partyID, claimantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	now := wpClock()
	_ = writeLease(ctx, partyID, hostLease{HostID: claimantID, Generation: newGen, ExpiresAt: now.Add(wpLeaseWindow)})
	return nil
}

// ── Playback state ────────────────────────────────────────────────────────

func writeState(ctx context.Context, id string, s WatchPartyState) error {
	b, _ := json.Marshal(s)
	return redisClient.Set(ctx, wpStateKey(id), b, wpStateTTL).Err()
}

// GetPartyState returns the current state. If the host lease has expired it
// atomically pauses and increments the version once (so an abandoned party
// stops), and reflects that in the returned state.
func GetPartyState(ctx context.Context, partyID string) (WatchPartyState, error) {
	v, err := redisClient.Get(ctx, wpStateKey(partyID)).Result()
	if errors.Is(err, redis.Nil) {
		return WatchPartyState{}, errWPNotFound
	}
	if err != nil {
		return WatchPartyState{}, err
	}
	var s WatchPartyState
	if json.Unmarshal([]byte(v), &s) != nil {
		return WatchPartyState{}, errWPNotFound
	}
	if !LeaseValid(ctx, partyID) && s.Action != "paused" {
		s.Action = "paused"
		s.Version++
		s.ServerTime = wpClock()
		_ = writeState(ctx, partyID, s)
	}
	return s, nil
}

// ApplyControl applies a host control. It rejects non-host actors, an expired
// lease, stale versions, and out-of-range/ nonfinite values; it advances the
// version and stamps server time.
func ApplyControl(ctx context.Context, partyID, actorID string, in WatchPartyControlInput) (WatchPartyState, error) {
	p, err := GetParty(ctx, partyID)
	if err != nil {
		return WatchPartyState{}, err
	}
	if p.HostID != actorID {
		return WatchPartyState{}, errWPNotHost
	}
	if !LeaseValid(ctx, partyID) {
		return WatchPartyState{}, errWPLeaseExpired
	}
	cur, err := GetPartyState(ctx, partyID)
	if err != nil {
		return WatchPartyState{}, err
	}
	if in.ExpectedVersion != cur.Version {
		return WatchPartyState{}, errWPStaleVersion
	}
	if err := validateControl(in); err != nil {
		return WatchPartyState{}, err
	}

	next := cur
	next.Version = cur.Version + 1
	next.ServerTime = wpClock()
	if l, ok := readLease(ctx, partyID); ok {
		next.LeaseExpiresAt = l.ExpiresAt
	}
	switch in.Action {
	case "play":
		next.Action = "playing"
		next.PositionSeconds = in.PositionSeconds
		next.PlaybackRate = in.PlaybackRate
	case "pause":
		next.Action = "paused"
		next.PositionSeconds = in.PositionSeconds
	case "seek":
		next.PositionSeconds = in.PositionSeconds
	case "change_media":
		next.MediaItemID = in.MediaItemID
		next.PositionSeconds = 0
		next.Action = "paused"
	default:
		return WatchPartyState{}, errWPBadControl
	}
	if err := writeState(ctx, partyID, next); err != nil {
		return WatchPartyState{}, err
	}
	return next, nil
}

func validateControl(in WatchPartyControlInput) error {
	if math.IsNaN(in.PositionSeconds) || math.IsInf(in.PositionSeconds, 0) || in.PositionSeconds < 0 {
		return errWPBadControl
	}
	switch in.Action {
	case "play":
		if math.IsNaN(in.PlaybackRate) || in.PlaybackRate < wpMinRate || in.PlaybackRate > wpMaxRate {
			return errWPBadControl
		}
	case "change_media":
		if !validJellyfinID(in.MediaItemID) {
			return errWPBadControl
		}
	case "pause", "seek":
		// position already validated
	default:
		return errWPBadControl
	}
	return nil
}

// PurgeUserWatchParties removes a user's Watch Party references (called from the
// account-lifecycle transaction). Hosted parties are ended and their state
// cleared; membership/invitations/offers referencing the user are removed.
func PurgeUserWatchParties(ctx context.Context, tx pgx.Tx, userID string) error {
	// End and clear Redis for parties this user hosts.
	rows, err := tx.Query(ctx, `SELECT id::text FROM watch_parties WHERE host_id=$1::uuid AND ended_at IS NULL`, userID)
	if err != nil {
		return err
	}
	var hosted []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		hosted = append(hosted, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range hosted {
		redisClient.Del(ctx, wpStateKey(id), wpLeaseKey(id), wpBeatKey(id))
	}
	for _, stmt := range []string{
		`UPDATE watch_parties SET ended_at=now() WHERE host_id=$1::uuid AND ended_at IS NULL`,
		`DELETE FROM watch_party_members WHERE user_id=$1::uuid`,
		`DELETE FROM watch_party_invitations WHERE invitee_id=$1::uuid`,
		`DELETE FROM watch_party_host_offers WHERE successor_id=$1::uuid`,
	} {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return err
		}
	}
	return nil
}
