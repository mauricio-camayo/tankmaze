import type { MatchSnapshot, TickUpdate } from '../types';

export type WsEvent =
  | { type: 'MATCH_SNAPSHOT'; payload: MatchSnapshot }
  | { type: 'TICK_UPDATE'; payload: TickUpdate }
  | { type: 'HIT'; payload: { victim: 'a' | 'b'; damage: number; remainingHP: number } }
  | { type: 'MATCH_OVER'; payload: { winner: 'a' | 'b' | null; reason: string; stats: unknown } }
  | { type: 'ERROR'; payload: { code: string; message: string } };

type EventHandler = (event: WsEvent) => void;

export class ObserverSocket {
  private ws: WebSocket | null = null;
  private handler: EventHandler | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private matchId: string = '';

  connect(matchId: string, onEvent: EventHandler) {
    this.matchId = matchId;
    this.handler = onEvent;
    this.openSocket();
  }

  seek(tick: number) {
    this.send({ action: 'REPLAY_SEEK', tick });
  }

  setSpeed(multiplier: number | 'step') {
    this.send({ action: 'REPLAY_SPEED', multiplier: String(multiplier) });
  }

  reobserve() {
    this.send({ action: 'OBSERVE', matchId: this.matchId });
  }

  disconnect() {
    this.handler = null;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
    this.ws?.close();
    this.ws = null;
  }

  private openSocket() {
    const endpoint = import.meta.env.VITE_WS_ENDPOINT as string;
    this.ws = new WebSocket(`${endpoint}?matchId=${encodeURIComponent(this.matchId)}`);

    this.ws.onopen = () => {
      this.send({ action: 'OBSERVE', matchId: this.matchId });
    };

    this.ws.onmessage = (e: MessageEvent) => {
      try {
        const msg = JSON.parse(e.data as string) as WsEvent;
        this.handler?.(msg);
      } catch {
        // ignore malformed frames
      }
    };

    this.ws.onclose = () => {
      if (this.handler) {
        this.reconnectTimer = setTimeout(() => this.openSocket(), 2000);
      }
    };
  }

  private send(payload: unknown) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(payload));
    }
  }
}
