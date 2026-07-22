export class AudioDubManager {
  constructor() { this.ctx = null; this.gains = new Map(); this.enabled = false; }

  enable() { if (!this.ctx) this.ctx = new (window.AudioContext || window.webkitAudioContext)(); this.enabled = true; }
  disable() { this.enabled = false; }

  registerRemote(id, audioEl) {
    if (!this.ctx) return;
    const src = this.ctx.createMediaElementSource(audioEl);
    const gain = this.ctx.createGain();
    gain.gain.value = 1;
    src.connect(gain); gain.connect(this.ctx.destination);
    this.gains.set(id, gain);
  }

  async playDub(speakerId, mimeType, b64) {
    if (!this.enabled || !this.ctx) return;
    try {
      const raw = atob(b64);
      const bytes = new Uint8Array(raw.length);
      for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
      const buf = await this.ctx.decodeAudioData(bytes.buffer);
      this._duck(speakerId, true);
      const src = this.ctx.createBufferSource();
      src.buffer = buf; src.connect(this.ctx.destination); src.start();
      src.onended = () => this._duck(speakerId, false);
    } catch (e) { console.warn('[audio] dub error', e); this._duck(speakerId, false); }
  }

  _duck(id, active) {
    const g = this.gains.get(id);
    if (g) g.gain.setTargetAtTime(active ? 0.1 : 1, this.ctx.currentTime, 0.05);
  }

  cleanup() { this.ctx?.close(); this.gains.clear(); }
}
