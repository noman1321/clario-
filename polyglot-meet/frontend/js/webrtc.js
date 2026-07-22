export class PeerManager {
  constructor(sig, localStream, onRemote, onRemoved) {
    this.sig = sig;
    this.localStream = localStream;
    this.onRemote = onRemote;
    this.onRemoved = onRemoved;
    this.peers = new Map();
    this.iceConfig = null;
  }

  async loadICEConfig() {
    try {
      const r = await fetch('/api/ice-config');
      this.iceConfig = await r.json();
    } catch {
      this.iceConfig = { iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] };
    }
  }

  async callParticipant(id) {
    const pc = this._create(id);
    const offer = await pc.createOffer({ offerToReceiveAudio: true, offerToReceiveVideo: true });
    await pc.setLocalDescription(offer);
    this.sig.send('offer', { targetId: id, data: { type: offer.type, sdp: offer.sdp } });
  }

  async handleOffer(senderId, offerData) {
    const pc = this._create(senderId);
    await pc.setRemoteDescription(new RTCSessionDescription(offerData));
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    this.sig.send('answer', { targetId: senderId, data: { type: answer.type, sdp: answer.sdp } });
  }

  async handleAnswer(senderId, answerData) {
    const pc = this.peers.get(senderId);
    if (pc) await pc.setRemoteDescription(new RTCSessionDescription(answerData));
  }

  async handleICE(senderId, candidate) {
    const pc = this.peers.get(senderId);
    if (pc && candidate) await pc.addIceCandidate(new RTCIceCandidate(candidate)).catch(() => {});
  }

  removePeer(id) {
    const pc = this.peers.get(id);
    if (pc) { pc.close(); this.peers.delete(id); this.onRemoved(id); }
  }

  // Swap the outgoing video track on every peer connection (screen share).
  replaceVideoTrack(track) {
    this.peers.forEach(pc => {
      const sender = pc.getSenders().find(s => s.track?.kind === 'video');
      sender?.replaceTrack(track);
    });
  }

  closeAll() { this.peers.forEach(pc => pc.close()); this.peers.clear(); }

  _create(id) {
    this.peers.get(id)?.close();
    const pc = new RTCPeerConnection(this.iceConfig || {});
    this.peers.set(id, pc);
    this.localStream?.getTracks().forEach(t => pc.addTrack(t, this.localStream));
    pc.onicecandidate = (e) => {
      if (e.candidate) this.sig.send('ice', { targetId: id, data: e.candidate.toJSON() });
    };
    pc.ontrack = (e) => {
      const stream = e.streams[0] || new MediaStream([e.track]);
      this.onRemote(id, stream);
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') this.peers.delete(id);
    };
    return pc;
  }
}

/**
 * VAD-based AudioRecorder.
 *
 * Instead of sending fixed-interval chunks (which includes silence → hallucination),
 * this uses the Web Audio API AnalyserNode to detect actual speech before recording.
 *
 * Flow:
 *   mic → AnalyserNode → RMS check every 50ms
 *   speech detected  → start MediaRecorder
 *   silence for 600ms OR maxDuration reached → stop + send chunk
 *
 * This eliminates:
 *   - Silent chunks (stops hallucination)
 *   - Fragmented WebM (we send complete utterances)
 *   - Wasted API calls on noise
 */
export class AudioRecorder {
  constructor(stream, onChunk, maxDurationMs = 8000) {
    this.stream      = stream;
    this.onChunk     = onChunk;
    this.maxDuration = maxDurationMs; // hard cap per segment

    // VAD tuning
    this.SILENCE_MS  = 700;   // ms of quiet → end of utterance
    this.THRESHOLD   = 0.018; // RMS amplitude (0–1); raise if noisy room
    this.MIN_BYTES   = 6000;  // skip chunks smaller than this (~0.4s of webm audio)
    this.MIN_GAP_MS  = 2500;  // min gap between sends — Groq free tier is 20 req/min
    this._lastSend   = 0;

    this._running      = false;
    this._paused       = false;
    this._rec          = null;
    this._chunks       = [];
    this._recStart     = 0;
    this._lastSpeech   = 0;
    this._analyser     = null;
    this._vadCtx       = null;
    this.mimeType      = this._pick();
  }

  _pick() {
    const types = ['audio/webm;codecs=opus', 'audio/webm', 'audio/ogg;codecs=opus', 'audio/mp4'];
    for (const t of types) {
      if (typeof MediaRecorder !== 'undefined' && MediaRecorder.isTypeSupported(t)) return t;
    }
    return '';
  }

  _toB64(bytes) {
    let bin = '';
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }

  _setupVAD() {
    try {
      this._vadCtx  = new AudioContext();
      const src     = this._vadCtx.createMediaStreamSource(this.stream);
      this._analyser = this._vadCtx.createAnalyser();
      this._analyser.fftSize = 512;
      this._analyser.smoothingTimeConstant = 0.8;
      src.connect(this._analyser);
    } catch (e) {
      console.warn('[VAD] AudioContext failed, falling back to timed mode', e);
    }
  }

  _getRMS() {
    if (!this._analyser) return 1; // no analyser → assume speaking (timed fallback)
    const buf = new Float32Array(this._analyser.fftSize);
    this._analyser.getFloatTimeDomainData(buf);
    let sum = 0;
    for (const v of buf) sum += v * v;
    return Math.sqrt(sum / buf.length);
  }

  _startRec() {
    if (this._rec && this._rec.state === 'recording') return;
    const audio = new MediaStream(this.stream.getAudioTracks());
    this._chunks   = [];
    this._recStart = Date.now();
    try {
      this._rec = new MediaRecorder(audio, this.mimeType ? { mimeType: this.mimeType } : undefined);
    } catch {
      this._rec = new MediaRecorder(audio);
      this.mimeType = this._rec.mimeType;
    }
    this._rec.ondataavailable = (e) => { if (e.data?.size > 0) this._chunks.push(e.data); };
    this._rec.onstop = async () => {
      const saved = this._chunks.splice(0);
      this._rec   = null;
      if (saved.length === 0) return;
      const blob  = new Blob(saved, { type: this.mimeType || 'audio/webm' });
      const bytes = new Uint8Array(await blob.arrayBuffer());
      const now   = Date.now();
      if (bytes.length >= this.MIN_BYTES && (now - this._lastSend) >= this.MIN_GAP_MS) {
        this._lastSend = now;
        this.onChunk(this._toB64(bytes), this.mimeType || 'audio/webm');
      }
    };
    // collect data every 100ms so we get data even on short utterances
    this._rec.start(100);
  }

  _stopRec() {
    if (this._rec && this._rec.state === 'recording') {
      this._rec.stop();
    }
  }

  _vadTick() {
    if (!this._running) return;

    if (!this._paused) {
      const rms       = this._getRMS();
      const now       = Date.now();
      const speaking  = rms > this.THRESHOLD;

      if (speaking) {
        this._lastSpeech = now;
        this._startRec();
      }

      if (this._rec && this._rec.state === 'recording') {
        const silent  = (now - this._lastSpeech)  > this.SILENCE_MS;
        const tooLong = (now - this._recStart)    >= this.maxDuration;
        if (silent || tooLong) {
          this._stopRec();
        }
      }
    }

    setTimeout(() => this._vadTick(), 50);
  }

  start() {
    if (!this.stream || this._running) return;
    this._running    = true;
    this._paused     = false;
    this._lastSpeech = Date.now();
    this._setupVAD();
    this._vadTick();
  }

  stop() {
    this._running = false;
    this._stopRec();
    if (this._vadCtx) { this._vadCtx.close(); this._vadCtx = null; }
  }

  pause() {
    this._paused = true;
    this._stopRec(); // stop current recording immediately on mute
  }

  resume() {
    if (!this._paused) return;
    this._paused     = false;
    this._lastSpeech = Date.now();
  }
}
