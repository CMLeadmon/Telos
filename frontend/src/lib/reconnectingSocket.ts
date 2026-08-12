/**
 * Shared reconnecting WebSocket controller.
 * Handles exponential backoff (500ms -> 30s) with ±20% jitter,
 * online/visibility listening, and clean lifecycle management.
 */

export interface ReconnectingSocketOptions {
  url: string;
  onOpen?: () => void;
  onMessage?: (data: string) => void;
  onClose?: (code: number, reason: string) => void;
  onError?: (error: Event) => void;
  minDelayMs?: number;
  maxDelayMs?: number;
  /**
   * Subprotocols for the handshake, resolved per attempt. A token-mode client
   * authenticates its upgrade with a single-use ticket, which cannot be captured
   * once at construction: it expires in 30 seconds and is spent by the first
   * handshake, so every reconnect has to fetch its own.
   */
  protocols?: () => Promise<string[]>;
}

export class ReconnectingSocket {
  private url: string;
  private ws: WebSocket | null = null;
  private minDelay: number;
  private maxDelay: number;
  private currentDelay: number;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private isIntentionallyClosed = false;
  private isConnected = false;
  private isConnecting = false;

  private onlineHandler?: () => void;
  private visibilityHandler?: () => void;

  private onOpen?: () => void;
  private onMessage?: (data: string) => void;
  private onClose?: (code: number, reason: string) => void;
  private onError?: (error: Event) => void;
  private protocols?: () => Promise<string[]>;

  constructor(options: ReconnectingSocketOptions) {
    this.url = options.url;
    this.minDelay = options.minDelayMs ?? 500;
    this.maxDelay = options.maxDelayMs ?? 30000;
    this.currentDelay = this.minDelay;
    this.onOpen = options.onOpen;
    this.onMessage = options.onMessage;
    this.onClose = options.onClose;
    this.onError = options.onError;
    this.protocols = options.protocols;

    this.installLifecycleListeners();
    void this.connect();
  }

  public async connect(): Promise<void> {
    if (this.isIntentionallyClosed || this.ws) return;
    // Claim the attempt before awaiting. The supplier makes a network round
    // trip, and without a marker a revive or a fired backoff timer landing in
    // that window would start a second attempt against the same null `ws`,
    // leaving one socket orphaned with no onclose wired to reconnect it.
    if (this.isConnecting) return;
    this.isConnecting = true;

    try {
      const protocols = this.protocols ? await this.protocols() : [];
      // The supplier awaited a round trip; the socket may have been closed, or
      // already reconnected, while it was in flight.
      if (this.isIntentionallyClosed || this.ws) return;
      this.ws = protocols.length
        ? new WebSocket(this.url, protocols)
        : new WebSocket(this.url);
    } catch {
      // A ticket the node refused to issue is a failed connection attempt, not
      // a crash: back off and try again like any other transport failure.
      this.scheduleReconnect();
      return;
    } finally {
      this.isConnecting = false;
    }

    this.ws.onopen = () => {
      this.isConnected = true;
      this.currentDelay = this.minDelay;
      this.onOpen?.();
    };

    this.ws.onmessage = (event: MessageEvent) => {
      this.onMessage?.(String(event.data));
    };

    this.ws.onerror = (event: Event) => {
      this.onError?.(event);
    };

    this.ws.onclose = (event: CloseEvent) => {
      this.ws = null;
      this.isConnected = false;
      this.onClose?.(event.code, event.reason);

      if (!this.isIntentionallyClosed) {
        this.scheduleReconnect();
      }
    };
  }

  public send(data: string): boolean {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data);
      return true;
    }
    return false;
  }

  public close(): void {
    this.isIntentionallyClosed = true;
    this.removeLifecycleListeners();
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
  }

  private installLifecycleListeners(): void {
    if (typeof window === "undefined") return;
    this.onlineHandler = () => this.reviveNow();
    this.visibilityHandler = () => {
      if (document.visibilityState === "visible") this.reviveNow();
    };
    window.addEventListener("online", this.onlineHandler);
    document.addEventListener("visibilitychange", this.visibilityHandler);
  }

  private removeLifecycleListeners(): void {
    if (typeof window === "undefined") return;
    if (this.onlineHandler) {
      window.removeEventListener("online", this.onlineHandler);
      this.onlineHandler = undefined;
    }
    if (this.visibilityHandler) {
      document.removeEventListener("visibilitychange", this.visibilityHandler);
      this.visibilityHandler = undefined;
    }
  }

  /**
   * A regained network or a foregrounded tab is positive evidence the transport
   * may work again, which makes any accumulated backoff stale. Drop the pending
   * timer, reset to the floor, and retry now rather than serving out a delay
   * that was earned while the device was offline.
   */
  private reviveNow(): void {
    if (this.isIntentionallyClosed || this.isConnected) return;
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.currentDelay = this.minDelay;
    void this.connect();
  }

  private scheduleReconnect(): void {
    if (this.timer || this.isIntentionallyClosed) return;

    // Calculate delay with ±20% jitter
    const jitter = (Math.random() * 0.4 - 0.2) * this.currentDelay;
    const delay = Math.min(this.maxDelay, Math.max(this.minDelay, this.currentDelay + jitter));

    // Next delay doubles up to maxDelay
    this.currentDelay = Math.min(this.maxDelay, this.currentDelay * 2);

    this.timer = setTimeout(() => {
      this.timer = null;
      void this.connect();
    }, delay);
  }
}
