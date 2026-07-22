export class SignalingClient extends EventTarget {
  constructor(url) {
    super();
    this.url = url;
    this.ws = null;
    this.ready = false;
    this._queue = [];
  }

  connect() {
    return new Promise((resolve, reject) => {
      this.ws = new WebSocket(this.url);
      this.ws.onopen = () => { this.ready = true; this._flush(); resolve(); };
      this.ws.onerror = () => reject(new Error('WebSocket failed'));
      this.ws.onclose = (e) => { this.ready = false; this.dispatchEvent(new CustomEvent('disconnected', { detail: e })); };
      this.ws.onmessage = (e) => {
        try {
          const msg = JSON.parse(e.data);
          this.dispatchEvent(new CustomEvent('message', { detail: msg }));
          this.dispatchEvent(new CustomEvent(msg.type, { detail: msg }));
        } catch {}
      };
    });
  }

  send(type, data) {
    const msg = JSON.stringify({ type, data });
    if (this.ready && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(msg);
    } else {
      this._queue.push(msg);
    }
  }

  _flush() {
    while (this._queue.length) {
      if (this.ws.readyState === WebSocket.OPEN) this.ws.send(this._queue.shift());
      else break;
    }
  }

  close() { this.ws?.close(); }
}
