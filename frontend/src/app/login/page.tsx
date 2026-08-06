"use client";

import { FormEvent, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Eye, EyeOff } from "lucide-react";
import { useAuthStore } from "@/stores/useAuthStore";
import { ApiError } from "@/lib/api";
import { BrandLogo } from "@/components/BrandLogo";
import { useThemeStore } from "@/stores/useThemeStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";

type Mode = "login" | "bootstrap" | "invite";
type FieldName = "username" | "password" | "token";
type FeedbackKind = "progress" | "success" | "error";

interface Feedback {
  kind: FeedbackKind;
  message: string;
  invalidFields: FieldName[];
  focusField?: FieldName;
}

const MODE_COPY: Record<Mode, { kicker: string; cta: string }> = {
  login: { kicker: "// sign in to your node", cta: "Enter the node" },
  bootstrap: { kicker: "// first boot — claim ownership", cta: "Bootstrap owner" },
  invite: { kicker: "// redeem your invite", cta: "Join the node" },
};

const AUTH_TIMEOUT_MS = 12_000;
const SUCCESS_REDIRECT_MS = 600;

const progressCopy = (mode: Mode): string => {
  if (mode === "login") return "verifying signal — hold the line.";
  if (mode === "bootstrap") return "claiming this node — hold the line.";
  return "redeeming invite — hold the line.";
};

const validate = (
  mode: Mode,
  username: string,
  password: string,
  token: string,
): Feedback | null => {
  const normalizedUsername = username.trim();
  const requiredFields: FieldName[] = [];
  if (!normalizedUsername) requiredFields.push("username");
  if (!password) requiredFields.push("password");
  if (mode !== "login" && !token.trim()) requiredFields.push("token");
  if (requiredFields.length > 0) {
    return {
      kind: "error",
      message: "transmission incomplete — fill every required field.",
      invalidFields: requiredFields,
      focusField: requiredFields[0],
    };
  }

  if (
    mode !== "login" &&
    (normalizedUsername.length < 3 || normalizedUsername.length > 32)
  ) {
    return {
      kind: "error",
      message: "identity rejected — username must be 3–32 characters.",
      invalidFields: ["username"],
      focusField: "username",
    };
  }

  if (mode !== "login" && (password.length < 15 || password.length > 128)) {
    return {
      kind: "error",
      message: "passphrase rejected — use 15–128 characters.",
      invalidFields: ["password"],
      focusField: "password",
    };
  }

  return null;
};

const errorFeedback = (mode: Mode, error: unknown): Feedback => {
  const status = error instanceof ApiError ? error.status : -1;
  const detail = error instanceof Error ? error.message.toLowerCase() : "";

  if (status === 0) {
    return {
      kind: "error",
      message:
        "node unreachable — check your connection and the server address, then try again.",
      invalidFields: [],
    };
  }
  if (status === 408 || detail.includes("timed out")) {
    return {
      kind: "error",
      message: "signal timed out — check your connection and try again.",
      invalidFields: [],
    };
  }
  if (status === 429) {
    return {
      kind: "error",
      message: "too many attempts — stand down briefly, then try again.",
      invalidFields: [],
    };
  }
  if (detail.includes("account disabled")) {
    return {
      kind: "error",
      message: "access denied — this account is disabled. Contact your node owner.",
      invalidFields: ["username"],
      focusField: "username",
    };
  }
  if (status === 401) {
    return {
      kind: "error",
      message: "credentials rejected — check your username and passphrase.",
      invalidFields: ["username", "password"],
      focusField: "password",
    };
  }
  if (detail.includes("username") && detail.includes("taken")) {
    return {
      kind: "error",
      message: "identity occupied — choose another username.",
      invalidFields: ["username"],
      focusField: "username",
    };
  }
  if (detail.includes("username")) {
    return {
      kind: "error",
      message: "identity rejected — username must be 3–32 characters.",
      invalidFields: ["username"],
      focusField: "username",
    };
  }
  if (detail.includes("password")) {
    return {
      kind: "error",
      message: "passphrase rejected — use 15–128 characters.",
      invalidFields: ["password"],
      focusField: "password",
    };
  }
  if (mode === "bootstrap" && detail.includes("token")) {
    return {
      kind: "error",
      message: "bootstrap token rejected — check the token and try again.",
      invalidFields: ["token"],
      focusField: "token",
    };
  }
  if (mode === "invite" && detail.includes("already used")) {
    return {
      kind: "error",
      message: "invite already spent — ask your node owner for a new one.",
      invalidFields: ["token"],
      focusField: "token",
    };
  }
  if (mode === "invite" && detail.includes("token")) {
    return {
      kind: "error",
      message: "invite rejected — the token is invalid or expired.",
      invalidFields: ["token"],
      focusField: "token",
    };
  }
  if (mode === "bootstrap" && detail.includes("already bootstrapped")) {
    return {
      kind: "error",
      message: "node already claimed — sign in or contact the node owner.",
      invalidFields: [],
    };
  }
  if (status === 403) {
    return {
      kind: "error",
      message: "request refused — refresh the page and try again.",
      invalidFields: [],
    };
  }
  if (status >= 500) {
    return {
      kind: "error",
      message: "node fault — the server could not complete the request. Try again.",
      invalidFields: [],
    };
  }
  return {
    kind: "error",
    message: "signal lost — check your entries and try again.",
    invalidFields: [],
  };
};

export default function LoginPage() {
  // The ink mockups set the wordmark in title case, the synthwave ones in caps.
  const wordmark = useThemeStore((s) => (s.theme === "ink" ? "Telos" : "TELOS"));
  const router = useRouter();
  const { status, fetchMe, login, bootstrap, acceptInvite } = useAuthStore();
  const [mode, setMode] = useState<Mode>("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [busy, setBusy] = useState(false);
  const usernameRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const tokenRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (status === "unknown") void fetchMe();
    if (status === "authenticated" && !busy) router.replace("/chat/");
  }, [status, busy, fetchMe, router]);

  const focusField = (field?: FieldName) => {
    if (field === "username") usernameRef.current?.focus();
    if (field === "password") passwordRef.current?.focus();
    if (field === "token") tokenRef.current?.focus();
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;

    const validationFeedback = validate(mode, username, password, token);
    if (validationFeedback) {
      setFeedback(validationFeedback);
      focusField(validationFeedback.focusField);
      return;
    }

    setBusy(true);
    setFeedback({
      kind: "progress",
      message: progressCopy(mode),
      invalidFields: [],
    });
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      AUTH_TIMEOUT_MS,
    );

    try {
      if (mode === "login")
        await login(username, password, controller.signal);
      else if (mode === "bootstrap")
        await bootstrap(username, password, token.trim(), controller.signal);
      else
        await acceptInvite(username, password, token.trim(), controller.signal);
      window.clearTimeout(timeout);
      setFeedback({
        kind: "success",
        message: "access accepted — entering the node.",
        invalidFields: [],
      });
      await new Promise((resolve) =>
        window.setTimeout(resolve, SUCCESS_REDIRECT_MS),
      );
      router.replace("/chat/");
    } catch (err) {
      window.clearTimeout(timeout);
      const nextFeedback = errorFeedback(mode, err);
      setFeedback(nextFeedback);
      window.requestAnimationFrame(() => focusField(nextFeedback.focusField));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="authwrap" data-testid="auth-page">
      <VaporwaveScene />
      <form className="authcard" onSubmit={submit} noValidate>
        <div className="lockup">
          <BrandLogo size={67} />
          <span className="word">{wordmark}</span>
        </div>
        <span className="kicker" style={{ textAlign: "center" }}>
          {MODE_COPY[mode].kicker}
        </span>

        <div className="field">
          <label htmlFor="username">username</label>
          <input
            id="username"
            ref={usernameRef}
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            minLength={mode === "login" ? undefined : 3}
            maxLength={mode === "login" ? undefined : 32}
            aria-invalid={
              feedback?.invalidFields.includes("username") || undefined
            }
            aria-describedby={
              feedback?.invalidFields.includes("username")
                ? "auth-feedback"
                : undefined
            }
            required
          />
        </div>
        <div className="field">
          <label htmlFor="password">password</label>
          <div className="passwordinput">
            <input
              id="password"
              ref={passwordRef}
              type={showPassword ? "text" : "password"}
              autoComplete={
                mode === "login" ? "current-password" : "new-password"
              }
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={mode === "login" ? undefined : 15}
              maxLength={mode === "login" ? undefined : 128}
              aria-invalid={
                feedback?.invalidFields.includes("password") || undefined
              }
              aria-describedby={
                feedback?.invalidFields.includes("password")
                  ? "auth-feedback"
                  : undefined
              }
              required
            />
            <button
              className="passwordtoggle"
              type="button"
              aria-label={showPassword ? "Hide password" : "Show password"}
              aria-pressed={showPassword}
              onClick={() => setShowPassword((visible) => !visible)}
            >
              {showPassword ? (
                <EyeOff aria-hidden="true" />
              ) : (
                <Eye aria-hidden="true" />
              )}
            </button>
          </div>
        </div>
        {mode !== "login" && (
          <div className="field">
            <label htmlFor="token">
              {mode === "bootstrap" ? "bootstrap token" : "invite token"}
            </label>
            <input
              id="token"
              ref={tokenRef}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="one-time-code"
              spellCheck={false}
              aria-invalid={
                feedback?.invalidFields.includes("token") || undefined
              }
              aria-describedby={
                feedback?.invalidFields.includes("token")
                  ? "auth-feedback"
                  : undefined
              }
              required
            />
          </div>
        )}

        {feedback && (
          <div
            id="auth-feedback"
            className={`authfeedback ${feedback.kind}`}
            role={feedback.kind === "error" ? "alert" : "status"}
            aria-live={feedback.kind === "error" ? "assertive" : "polite"}
            aria-atomic="true"
          >{`// ${feedback.message}`}</div>
        )}

        <button
          className="btn rose btn-lg"
          type="submit"
          disabled={busy}
          aria-busy={busy}
        >
          {busy
            ? mode === "invite"
              ? "Joining…"
              : "Working…"
            : MODE_COPY[mode].cta}
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
                  setFeedback(null);
                  setShowPassword(false);
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
