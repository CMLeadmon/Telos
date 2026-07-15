"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuthStore } from "@/stores/useAuthStore";
import { BrandLogo } from "@/components/BrandLogo";
import { VaporwaveScene } from "@/components/VaporwaveScene";

type Mode = "login" | "bootstrap" | "invite";

const MODE_COPY: Record<Mode, { kicker: string; cta: string }> = {
  login: { kicker: "// sign in to your node", cta: "Enter the node" },
  bootstrap: { kicker: "// first boot — claim ownership", cta: "Bootstrap owner" },
  invite: { kicker: "// redeem your invite", cta: "Join the node" },
};

export default function LoginPage() {
  const router = useRouter();
  const { status, fetchMe, login, bootstrap, acceptInvite } = useAuthStore();
  const [mode, setMode] = useState<Mode>("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (status === "unknown") void fetchMe();
    if (status === "authenticated") router.replace("/chat/");
  }, [status, fetchMe, router]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      if (mode === "login") await login(username, password);
      else if (mode === "bootstrap") await bootstrap(username, password, token);
      else await acceptInvite(username, password, token);
      router.replace("/chat/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "something went wrong");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="authwrap" data-testid="auth-page">
      <VaporwaveScene />
      <form className="authcard" onSubmit={submit}>
        <div className="lockup">
          <BrandLogo size={67} />
          <span className="word">TELOS</span>
        </div>
        <span className="kicker" style={{ textAlign: "center" }}>
          {MODE_COPY[mode].kicker}
        </span>

        <div className="field">
          <label htmlFor="username">username</label>
          <input
            id="username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="password">password</label>
          <input
            id="password"
            type="password"
            autoComplete={mode === "login" ? "current-password" : "new-password"}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </div>
        {mode !== "login" && (
          <div className="field">
            <label htmlFor="token">
              {mode === "bootstrap" ? "bootstrap token" : "invite token"}
            </label>
            <input
              id="token"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              required
            />
          </div>
        )}

        {error && <div className="formerr">{`// ${error}`}</div>}

        <button className="btn rose btn-lg" type="submit" disabled={busy}>
          {busy ? "…" : MODE_COPY[mode].cta}
        </button>

        <div
          className="mono"
          style={{
            display: "flex",
            justifyContent: "center",
            gap: 18,
            fontSize: 11,
          }}
        >
          {(["login", "bootstrap", "invite"] as Mode[])
            .filter((m) => m !== mode)
            .map((m) => (
              <button
                key={m}
                type="button"
                style={{ color: "var(--accent)" }}
                onClick={() => {
                  setMode(m);
                  setError(null);
                }}
              >
                {m === "login"
                  ? "sign in"
                  : m === "bootstrap"
                    ? "first boot"
                    : "have an invite?"}
              </button>
            ))}
        </div>
        <span
          className="kicker"
          style={{ textAlign: "center", fontSize: 10.5 }}
        >
          be on the net, but not of the net
        </span>
      </form>
    </div>
  );
}
