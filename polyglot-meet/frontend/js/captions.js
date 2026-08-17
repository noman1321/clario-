export class CaptionManager {
  constructor(historyEl) {
    this.historyEl = historyEl;
    this.tileMap   = new Map();
    this.timers    = new Map();

    this._bar      = document.getElementById('liveCaptionBar');
    this._barText  = document.getElementById('captionBarText');
    this._barName  = document.getElementById('captionBarName');
    this._barAvatar= document.getElementById('captionBarAvatar');
    this._barTimer = null;

    this._panel    = document.getElementById('captionPanel');
    this._panelList= document.getElementById('captionPanelList');
    this._lastSpeaker = null;
    this._barSpeaker  = null;
    this._barParts    = [];
    this.liveEnabled  = true; // live overlay; history always records
  }

  registerTile(id, tileEl)   { this.tileMap.set(id, tileEl); }
  unregisterTile(id)         { this.tileMap.delete(id); }

  setLiveEnabled(on) {
    this.liveEnabled = !!on;
    if (!on) this.hideBar();
  }

  setTranslating(speakerId, active) {
    const tile = this.tileMap.get(speakerId);
    if (!tile) return;
    tile.querySelector('.tile-translating')?.classList.toggle('show', active);
    tile.classList.toggle('translating', active);
    tile.classList.toggle('speaking', active);
  }

  showCaption(speakerId, speakerName, text, isFinal) {
    if (!text) return;

    // 1. Live caption bar (when enabled)
    if (this.liveEnabled) this._showBar(speakerName, text);

    // 2. Tile overlay
    const tile = this.tileMap.get(speakerId);
    if (tile && this.liveEnabled) {
      const el = tile.querySelector('.tile-caption');
      if (el) {
        el.textContent = text;
        el.classList.add('show');
        el.classList.remove('caption-fade-out');
        clearTimeout(this.timers.get(speakerId));
        if (isFinal) {
          this.timers.set(speakerId, setTimeout(() => {
            el.classList.add('caption-fade-out');
            setTimeout(() => {
              el.classList.remove('show', 'caption-fade-out');
              el.textContent = '';
            }, 800);
          }, 4000));
        }
      }
    }

    // 3. History always gets finals (even in Audio/dub mode)
    if (isFinal) this._addPanelEntry(speakerName, text);
  }

  _showBar(name, text) {
    if (!this._bar || !this._barText) return;

    // Append consecutive lines from same speaker so captions read as speech
    if (name === this._barSpeaker && this._barParts.length) {
      const last = this._barParts[this._barParts.length - 1];
      if (last !== text) this._barParts.push(text);
      // Keep last 2 phrases visible for context
      if (this._barParts.length > 2) this._barParts = this._barParts.slice(-2);
      this._barText.textContent = this._barParts.join(' ');
    } else {
      this._barSpeaker = name;
      this._barParts = [text];
      this._barText.textContent = text;
    }

    this._barName.textContent  = name;
    this._barAvatar.textContent = name[0]?.toUpperCase() || '?';
    this._bar.classList.add('visible');

    clearTimeout(this._barTimer);
    this._barTimer = setTimeout(() => {
      this._bar.classList.remove('visible');
      this._barSpeaker = null;
      this._barParts = [];
    }, 7000);
  }

  hideBar() {
    clearTimeout(this._barTimer);
    this._bar?.classList.remove('visible');
    this._barSpeaker = null;
    this._barParts = [];
  }

  openPanel()  { this._panel?.classList.add('open'); }
  closePanel() { this._panel?.classList.remove('open'); }
  togglePanel(){ this._panel?.classList.toggle('open'); }

  _addPanelEntry(name, text) {
    if (!this._panelList) return;

    this._panelList.querySelector('.cap-empty')?.remove();

    const now   = new Date();
    const timeStr = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

    if (name === this._lastSpeaker) {
      const lastGroup = this._panelList.querySelector('.cap-group:last-child');
      if (lastGroup) {
        const bubble = document.createElement('div');
        bubble.className = 'cap-bubble';
        bubble.textContent = text;
        lastGroup.querySelector('.cap-bubbles').appendChild(bubble);
        lastGroup.querySelector('.cap-time').textContent = timeStr;
        this._panelList.scrollTop = this._panelList.scrollHeight;
        return;
      }
    }

    this._lastSpeaker = name;
    const group = document.createElement('div');
    group.className = 'cap-group';
    group.innerHTML = `
      <div class="cap-group-header">
        <div class="cap-avatar">${esc(name[0]?.toUpperCase() || '?')}</div>
        <span class="cap-speaker-name">${esc(name)}</span>
        <span class="cap-time">${timeStr}</span>
      </div>
      <div class="cap-bubbles">
        <div class="cap-bubble">${esc(text)}</div>
      </div>`;
    this._panelList.appendChild(group);
    this._panelList.scrollTop = this._panelList.scrollHeight;

    while (this._panelList.querySelectorAll('.cap-group').length > 100) {
      this._panelList.querySelector('.cap-group')?.remove();
    }

    if (this.historyEl) {
      const item = document.createElement('div');
      item.className = 'cap-line';
      item.innerHTML = `<span class="cap-speaker">${esc(name)}:</span><span class="cap-text"> ${esc(text)}</span>`;
      this.historyEl.appendChild(item);
      this.historyEl.scrollTop = this.historyEl.scrollHeight;
      while (this.historyEl.children.length > 50) this.historyEl.removeChild(this.historyEl.firstChild);
    }
  }
}

function esc(s) { return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
