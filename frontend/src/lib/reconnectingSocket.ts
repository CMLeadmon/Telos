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

  private onOpen?: () => void;
  private onMessage?: (data: string) => void;
  private onClose?: (code: number, reason: string) => void;
  private onError?: (error: Event) => void;

  constructor(options: ReconnectingSocketOptions) {
    this.url = options.url;
    this.minDelay = options.minDelayMs ?? 500;
    this.maxDelay = options.maxDelayMs ?? 30000;
    this.currentDelay = this.minDelay;
    this.onOpen = options.onOpen;
    this.onMessage = options.onMessage;
    this.onClose = options.onClose;
    this.onError = options.onError;

    this.connect();
  }

  public connect(): void {
    if (this.isIntentionallyClosed || this.ws) return;

    try {
      this.ws = new WebSocket(this.url);
    } catch {
      this.scheduleReconnect();
      return;
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
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
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
      this.connect();
    }, delay);
  }
}
