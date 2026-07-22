export class CaptionManager {
  constructor(historyEl) {
    // historyEl kept for compatibility but we primarily use the new panel
    this.historyEl = historyEl;
    this.tileMap   = new Map();
    this.timers    = new Map();

    // New: live caption bar elements
    this._bar      = document.getElementById('liveCaptionBar');
    this._barText  = document.getElementById('captionBarText');
    this._barName  = document.getElementById('captionBarName');
    this._barAvatar= document.getElementById('captionBarAvatar');
    this._barTimer = null;

    // New: caption history panel
    this._panel    = document.getElementById('captionPanel');
    this._panelList= document.getElementById('captionPanelList');
    this._lastSpeaker = null;
  }

  registerTile(id, tileEl)   { this.tileMap.set(id, tileEl); }
  unregisterTile(id)         { this.tileMap.delete(id); }

  setTranslating(speakerId, active) {
    const tile = this.tileMap.get(speakerId);
    if (!tile) return;
    tile.querySelector('.tile-translating')?.classList.toggle('show', active);
    tile.classList.toggle('translating', active);
    tile.classList.toggle('speaking', active);
  }

  showCaption(speakerId, speakerName, text, isFinal) {
    // 1. Update live caption bar (big, always visible)
    this._showBar(speakerName, text);

    // 2. Update tile overlay (secondary, small)
    const tile = this.tileMap.get(speakerId);
    if (tile) {
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
          }, 3000));
        }
      }
    }

    // 3. Add to scrollable caption panel on final
    if (isFinal && text) this._addPanelEntry(speakerName, text);
  }

  // ── Live Caption Bar ──────────────────────────────────────
  _showBar(name, text) {
    if (!this._bar || !this._barText) return;
    this._barText.textContent  = text;
    this._barName.textContent  = name;
    this._barAvatar.textContent = name[0]?.toUpperCase() || '?';
    this._bar.classList.add('visible');

    // Auto-hide after 5s of no new caption
    clearTimeout(this._barTimer);
    this._barTimer = setTimeout(() => {
      this._bar.classList.remove('visible');
    }, 5000);
  }

  hideBar() {
    clearTimeout(this._barTimer);
    this._bar?.classList.remove('visible');
  }

  // ── Caption History Panel ─────────────────────────────────
  openPanel()  { this._panel?.classList.add('open'); }
  closePanel() { this._panel?.classList.remove('open'); }
  togglePanel(){ this._panel?.classList.toggle('open'); }

  _addPanelEntry(name, text) {
    if (!this._panelList) return;

    // Remove empty state if present
    this._panelList.querySelector('.cap-empty')?.remove();

    const now   = new Date();
    const timeStr = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

    // Group consecutive messages from same speaker
    if (name === this._lastSpeaker) {
      // Append to existing group
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

    // New speaker group
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

    // Cap at 100 groups
    while (this._panelList.querySelectorAll('.cap-group').length > 100) {
      this._panelList.querySelector('.cap-group')?.remove();
    }

    // Also keep old history panel in sync
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

function esc(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
