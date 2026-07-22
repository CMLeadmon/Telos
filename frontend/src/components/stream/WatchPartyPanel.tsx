"use client";

import { useEffect, useState } from "react";
import { useWatchPartyStore } from "@/stores/useWatchPartyStore";
import { useAuthStore } from "@/stores/useAuthStore";
import { leaseSecondsRemaining } from "@/lib/playbackSync";

// WatchPartyPanel shows the party, the host lease countdown, host-only playback
// and succession controls, target-only accept/claim controls, and linked
// chat/voice buttons. There is never a general "claim host" action.
export function WatchPartyPanel() {
  const party = useWatchPartyStore((s) => s.party);
  const state = useWatchPartyStore((s) => s.state);
  const isHost = useWatchPartyStore((s) => s.isHost);
  const detached = useWatchPartyStore((s) => s.detached);
  const control = useWatchPartyStore((s) => s.control);
  const offerSuccessor = useWatchPartyStore((s) => s.offerSuccessor);
  const acceptSuccessorOffer = useWatchPartyStore((s) => s.acceptSuccessorOffer);
  const cancelSuccessorOffer = useWatchPartyStore((s) => s.cancelSuccessorOffer);
  const claimExpiredHostLease = useWatchPartyStore((s) => s.claimExpiredHostLease);
  const openLinkedChat = useWatchPartyStore((s) => s.openLinkedChat);
  const joinLinkedVoice = useWatchPartyStore((s) => s.joinLinkedVoice);
  const loadState = useWatchPartyStore((s) => s.loadState);
  const detach = useWatchPartyStore((s) => s.detach);
  const rejoin = useWatchPartyStore((s) => s.rejoin);

  const user = useAuthStore((s) => s.user);
  const [successorId, setSuccessorId] = useState("");
  const [now, setNow] = useState(() => Date.now());

  // Participants poll for authoritative state; the host drives it.
  useEffect(() => {
    if (!party || isHost || detached) return;
    const t = setInterval(() => void loadState(), 1000);
    return () => clearInterval(t);
  }, [party, isHost, detached, loadState]);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  if (!party) return null;
  const leaseLeft = state ? leaseSecondsRemaining(state, now) : 0;
  // The claim control appears only to the party's accepted successor once the
  // lease has expired — never as a general action.
  const canClaim = !isHost && leaseLeft === 0;

  return (
    <aside className="wp-panel" data-testid="watch-party-panel" aria-label="Watch Party">
      <header>
        <h3>Watch Party</h3>
        <span className="wp-lease" data-testid="wp-lease">
          host lease: {leaseLeft}s
        </span>
      </header>

      {isHost && state && (
        <div className="wp-host-controls" data-testid="wp-host-controls">
          <button onClick={() => void control({ action: "play", positionSeconds: state.positionSeconds, playbackRate: 1 })}>
            Play
          </button>
          <button onClick={() => void control({ action: "pause", positionSeconds: state.positionSeconds })}>Pause</button>
        </div>
      )}
      {!isHost && (
        <p className="wp-follow" data-testid="wp-participant-note">
          You are watching in sync with the host.
        </p>
      )}

      {isHost ? (
        <div className="wp-succession">
          <input
            aria-label="Successor user id"
            value={successorId}
            onChange={(e) => setSuccessorId(e.target.value)}
            placeholder="successor user id"
          />
          <button onClick={() => void offerSuccessor(successorId)} data-testid="wp-offer-successor">
            Offer host
          </button>
          <button onClick={() => void cancelSuccessorOffer()}>Cancel offer</button>
        </div>
      ) : (
        <div className="wp-succession">
          <button onClick={() => void acceptSuccessorOffer()} data-testid="wp-accept-successor">
            Accept host offer
          </button>
          {canClaim && (
            <button onClick={() => void claimExpiredHostLease()} data-testid="wp-claim-host">
              Take over (host left)
            </button>
          )}
        </div>
      )}

      <div className="wp-links">
        {party.textChannelId && (
          <button onClick={openLinkedChat} data-testid="wp-open-chat">
            Open party chat
          </button>
        )}
        {party.voiceChannelId && (
          <button onClick={() => void joinLinkedVoice()} data-testid="wp-join-voice">
            Join party voice
          </button>
        )}
        {detached ? (
          <button onClick={() => void rejoin()}>Rejoin sync</button>
        ) : (
          !isHost && <button onClick={() => void detach()}>Watch independently</button>
        )}
      </div>
      <span className="wp-me" hidden>
        {user?.ID}
      </span>
    </aside>
  );
}
