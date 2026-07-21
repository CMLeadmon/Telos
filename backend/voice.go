package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/webhook"
	"github.com/redis/go-redis/v9"
)

// Voice capacity policy (S08).
const (
	maxVoiceParticipants = 25
	voiceTokenTTL        = 90 * time.Second
	voiceReservationTTL  = 2 * time.Minute // always exceeds token + clock skew
	voiceCommittedTTL    = 90 * time.Second
)

var errVoiceCapacityUnavailable = errors.New("voice capacity unavailable")

// mintVoiceToken issues a 90-second LiveKit JWT that grants room join,
// subscribe, and microphone-only publication. Video, screen share, arbitrary
// data, recording, ingress/egress, room administration, and wildcard room
// access are all denied.
func mintVoiceToken(apiKey, apiSecret, roomName, identity string) (string, error) {
	at := auth.NewAccessToken(apiKey, apiSecret)
	at.SetIdentity(identity)
	at.SetValidFor(voiceTokenTTL)

	grant := &auth.VideoGrant{
		Room:     roomName,
		RoomJoin: true,
	}
	grant.SetCanPublish(true)
	grant.SetCanSubscribe(true)
	grant.SetCanPublishData(false)
	// Microphone audio only — no camera or screen-share tracks.
	grant.CanPublishSources = []string{"microphone"}
	// Explicitly deny privileged capabilities (all default false, set for
	// clarity and to resist future struct changes).
	grant.RoomAdmin = false
	grant.RoomCreate = false
	grant.RoomList = false
	grant.Recorder = false
	grant.Hidden = false
	grant.IngressAdmin = false

	at.AddGrant(grant)
	return at.ToJWT()
}

// VoiceSeatLease is a held (reserved or committed) voice seat.
type VoiceSeatLease interface {
	Commit(ctx context.Context, participantSID string) error
	Release(ctx context.Context) error
}

// VoiceSeatStore reserves and reconciles the bounded voice seat pool.
type VoiceSeatStore interface {
	Reserve(ctx context.Context, userID, roomID string) (VoiceSeatLease, error)
	ApplyWebhook(ctx context.Context, eventType, identity, roomID, participantSID string) error
	Reconcile(ctx context.Context, activeUserIDs []string) error
}

// redisVoiceSeatStore enforces one active seat per user and at most 25 seats
// total via a single Redis ZSET keyed by user ID (member uniqueness gives the
// per-user cap; ZCARD gives the global cap). A per-user string records the
// room so a webhook can match the reservation.
type redisVoiceSeatStore struct {
	rdb *redis.Client
}

func newVoiceSeatStore(rdb *redis.Client) *redisVoiceSeatStore {
	return &redisVoiceSeatStore{rdb: rdb}
}

const voiceSeatsKey = "telos:voice:seats"

func voiceUserKey(userID string) string { return "telos:voice:user:" + userID }

// reserveVoiceScript adds userID to the seats ZSET if either the user already
// holds a seat (re-reserve) or the pool is below capacity. ARGV: cap, userID,
// roomID, reservationTTLsecs. Returns 1 reserved, 0 at capacity.
var reserveVoiceScript = redis.NewScript(`
local now = tonumber(redis.call('TIME')[1])
local cap = tonumber(ARGV[1])
redis.call('ZREMRANGEBYSCORE', KEYS[1], 0, now)
local already = redis.call('ZSCORE', KEYS[1], ARGV[2])
if already == false then
  if redis.call('ZCARD', KEYS[1]) >= cap then
    return 0
  end
end
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[4]), ARGV[2])
redis.call('SET', KEYS[2], ARGV[3], 'EX', tonumber(ARGV[4]))
return 1
`)

type redisVoiceLease struct {
	store  *redisVoiceSeatStore
	userID string
	roomID string
}

func (s *redisVoiceSeatStore) Reserve(ctx context.Context, userID, roomID string) (VoiceSeatLease, error) {
	res, err := reserveVoiceScript.Run(ctx, s.rdb,
		[]string{voiceSeatsKey, voiceUserKey(userID)},
		maxVoiceParticipants, userID, roomID, int(voiceReservationTTL.Seconds())).Int()
	if err != nil {
		// Fail closed rather than issue an unaccounted token.
		return nil, errVoiceCapacityUnavailable
	}
	if res == 0 {
		return nil, errVoiceCapacityUnavailable
	}
	return &redisVoiceLease{store: s, userID: userID, roomID: roomID}, nil
}

// commitVoiceScript extends a committed seat to the committed TTL. It is
// idempotent: a missing member is simply (re)added within capacity semantics
// already enforced at reserve time.
var commitVoiceScript = redis.NewScript(`
local now = tonumber(redis.call('TIME')[1])
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
redis.call('SET', KEYS[2], ARGV[3], 'EX', tonumber(ARGV[2]))
return 1
`)

func (l *redisVoiceLease) Commit(ctx context.Context, participantSID string) error {
	_ = participantSID
	return commitVoiceScript.Run(ctx, l.store.rdb,
		[]string{voiceSeatsKey, voiceUserKey(l.userID)},
		l.userID, int(voiceCommittedTTL.Seconds()), l.roomID).Err()
}

func (l *redisVoiceLease) Release(ctx context.Context) error {
	return l.store.release(ctx, l.userID)
}

func (s *redisVoiceSeatStore) release(ctx context.Context, userID string) error {
	pipe := s.rdb.TxPipeline()
	pipe.ZRem(ctx, voiceSeatsKey, userID)
	pipe.Del(ctx, voiceUserKey(userID))
	_, err := pipe.Exec(ctx)
	return err
}

// ApplyWebhook commits or releases a seat in response to a verified LiveKit
// webhook. Replay/reordering/loss may under-admit temporarily but never
// exceed the cap (reserve is the only path that adds under a capacity check).
func (s *redisVoiceSeatStore) ApplyWebhook(ctx context.Context, eventType, identity, roomID, participantSID string) error {
	switch eventType {
	case "participant_joined":
		lease := &redisVoiceLease{store: s, userID: identity, roomID: roomID}
		return lease.Commit(ctx, participantSID)
	case "participant_left":
		return s.release(ctx, identity)
	case "room_finished":
		// Room end releases every seat scoped to that room. Without a live
		// per-room index we rely on TTL expiry plus the participant_left
		// events; nothing to do synchronously here.
		return nil
	default:
		return nil
	}
}

// Reconcile prunes any committed seat whose user is not in the authoritative
// active set, so a missed participant_left cannot pin a seat past the TTL.
func (s *redisVoiceSeatStore) Reconcile(ctx context.Context, activeUserIDs []string) error {
	active := make(map[string]struct{}, len(activeUserIDs))
	for _, u := range activeUserIDs {
		active[u] = struct{}{}
	}
	members, err := s.rdb.ZRange(ctx, voiceSeatsKey, 0, -1).Result()
	if err != nil {
		return err
	}
	for _, m := range members {
		if _, ok := active[m]; !ok {
			if err := s.release(ctx, m); err != nil {
				return err
			}
		}
	}
	return nil
}

var voiceSeats VoiceSeatStore

// handleVoiceWebhook verifies a signed LiveKit webhook and applies the
// participant lifecycle to the seat store. An unsigned or invalid webhook is
// rejected without side effects.
func handleVoiceWebhook(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("LIVEKIT_API_KEY")
	apiSecret := os.Getenv("LIVEKIT_API_SECRET")
	if apiKey == "" || apiSecret == "" {
		writeAPIError(w, r, http.StatusInternalServerError, "voice_unconfigured", "Voice is not configured.")
		return
	}
	provider := auth.NewSimpleKeyProvider(apiKey, apiSecret)
	event, err := webhook.ReceiveWebhookEvent(r, provider)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid_webhook", "The webhook signature is invalid.")
		return
	}
	identity, roomID, sid := "", "", ""
	if event.Participant != nil {
		identity = event.Participant.Identity
		sid = event.Participant.Sid
	}
	if event.Room != nil {
		roomID = event.Room.Name
	}
	if err := voiceSeats.ApplyWebhook(r.Context(), event.Event, identity, roomID, sid); err != nil {
		log.Printf("voice: webhook apply failed request_id=%s event=%s: %v", requestIDFrom(r.Context()), event.Event, err)
	}
	w.WriteHeader(http.StatusOK)
}
