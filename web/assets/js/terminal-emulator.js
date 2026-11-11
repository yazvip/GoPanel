const PALETTE = [
  '#0c0c0c', '#c50f1f', '#13a10e', '#c19c00', '#0037da', '#881798', '#3a96dd', '#cccccc',
  '#767676', '#e74856', '#16c60c', '#f9f1a5', '#3b78ff', '#b4009e', '#61d6d6', '#f2f2f2',
];

const DEFAULT_FG = '#d4d4d4';
const DEFAULT_BG = '#0b0e14';
const SCROLLBACK_DEFAULT = 2000;

function color256(index) {
  if (index < 16) return PALETTE[index] || DEFAULT_FG;
  if (index < 232) {
    const n = index - 16;
    const r = Math.floor(n / 36);
    const g = Math.floor((n % 36) / 6);
    const b = n % 6;
    const hex = (v) => (v === 0 ? 0 : 55 + v * 40).toString(16).padStart(2, '0');
    return `#${hex(r)}${hex(g)}${hex(b)}`;
  }
  if (index < 256) {
    const level = 8 + (index - 232) * 10;
    const hex = level.toString(16).padStart(2, '0');
    return `#${hex}${hex}${hex}`;
  }
  return DEFAULT_FG;
}

function blankCell() {
  return { ch: ' ', fg: null, bg: null };
}

function defaultAttr() {
  return {
    fg: null, bg: null, bold: false, dim: false, italic: false,
    underline: false, strike: false, reverse: false, hidden: false,
  };
}

function charWidth(ch) {
  const code = ch.codePointAt(0) || 0;
  if (code === 0) return 0;
  if (code < 32 || (code >= 0x7f && code < 0xa0)) return 0;
  if (
    (code >= 0x1100 && code <= 0x115f)
    || (code >= 0x2e80 && code <= 0xa4cf)
    || (code >= 0xac00 && code <= 0xd7a3)
    || (code >= 0xf900 && code <= 0xfaff)
    || (code >= 0xfe30 && code <= 0xfe4f)
    || (code >= 0xff00 && code <= 0xff60)
    || (code >= 0xffe0 && code <= 0xffe6)
    || (code >= 0x1f300 && code <= 0x1f64f)
    || (code >= 0x1f900 && code <= 0x1f9ff)
    || (code >= 0x20000 && code <= 0x3fffd)
  ) return 2;
  return 1;
}

function isCombining(code) {
  return (code >= 0x0300 && code <= 0x036f)
    || (code >= 0x1ab0 && code <= 0x1aff)
    || (code >= 0x1dc0 && code <= 0x1dff)
    || (code >= 0x20d0 && code <= 0x20ff)
    || (code >= 0xfe20 && code <= 0xfe2f);
}

export class TerminalEmulator {
  constructor({ cols = 80, rows = 24, scrollback = SCROLLBACK_DEFAULT, onData, onResize, onTitle } = {}) {
    this.cols = Math.max(2, cols);
    this.rows = Math.max(2, rows);
    this.maxScrollback = scrollback;
    this.onData = onData || null;
    this.onResize = onResize || null;
    this.onTitle = onTitle || null;
    this.scrollback = [];
    this.screen = this.newScreen(this.rows);
    this.cursor = { x: 0, y: 0 };
    this.savedCursor = null;
    this.attr = defaultAttr();
    this.scrollTop = 0;
    this.scrollBottom = this.rows - 1;
    this.wrapPending = false;
    this.autowrap = true;
    this.cursorVisible = true;
    this.viewOffset = 0;
    this.altActive = false;
    this.mainState = null;
    this.state = 'ground';
    this.csi = '';
    this.osc = '';
    this.charset = '';
    this.frame = 0;
    this.element = document.createElement('div');
    this.element.className = 'term-screen';
    this.element.setAttribute('role', 'application');
    this.element.setAttribute('aria-label', 'Terminal');
    this.render();
  }

  newScreen(rows) {
    return Array.from({ length: rows }, () => this.newRow());
  }

  newRow() {
    return Array.from({ length: this.cols }, blankCell);
  }

  newCells(width) {
    return Array.from({ length: width }, blankCell);
  }

  setSize(cols, rows) {
    this.cols = cols;
    this.rows = rows;
  }

  write(data) {
    if (!data) return;
    for (const ch of data) {
      this.consume(ch);
    }
    this.schedule();
  }

  consume(ch) {
    if (ch === '\x1b') {
      if (this.state === 'osc') { this.state = 'ground'; return; }
      if (this.state === 'str') { this.state = 'ground'; return; }
      this.state = 'escape';
      return;
    }
    if (this.state === 'escape') {
      this.handleEscape(ch);
      return;
    }
    if (this.state === 'csi') {
      const code = ch.charCodeAt(0);
      if (code >= 0x40 && code <= 0x7e) {
        this.handleCSI(ch);
        this.state = 'ground';
      } else if (this.csi.length < 128) {
        this.csi += ch;
      }
      return;
    }
    if (this.state === 'osc') {
      if (ch === '\x07') {
        this.handleOSC(this.osc);
        this.osc = '';
        this.state = 'ground';
      } else if (ch === '\x1b') {
        this.handleOSC(this.osc);
        this.osc = '';
        this.state = 'str';
      } else if (this.osc.length < 4096) {
        this.osc += ch;
      }
      return;
    }
    if (this.state === 'str') {
      if (ch === '\\') this.state = 'ground';
      return;
    }
    if (this.state === 'charset') {
      this.charset = ch;
      this.state = 'ground';
      return;
    }
    if (this.state === 'hash') {
      this.state = 'ground';
      return;
    }

    const code = ch.codePointAt(0);
    if (code === 0x1b) return;
    if (code === 0x0a || code === 0x0b || code === 0x0c) { this.lineFeed(); return; }
    if (code === 0x0d) { this.cursor.x = 0; this.wrapPending = false; return; }
    if (code === 0x08) {
      this.wrapPending = false;
      this.cursor.x = Math.max(0, this.cursor.x - 1);
      return;
    }
    if (code === 0x09) {
      const next = Math.min(this.cols - 1, (Math.floor(this.cursor.x / 8) + 1) * 8);
      this.cursor.x = next;
      this.wrapPending = false;
      return;
    }
    if (code === 0x07 || code === 0x00 || code === 0x0e || code === 0x0f) return;
    if (code < 0x20) return;
    if (isCombining(code)) {
      const x = Math.max(0, this.cursor.x - 1);
      const cell = this.screen[this.cursor.y][x];
      if (cell) cell.ch += ch;
      return;
    }
    this.putChar(ch);
  }

  putChar(ch) {
    const width = charWidth(ch);
    if (width === 0) return;
    if (this.wrapPending) {
      this.wrapPending = false;
      this.cursor.x = 0;
      this.lineFeed();
    }
    if (width === 2 && this.cursor.x >= this.cols - 1) {
      if (this.autowrap) {
        this.cursor.x = 0;
        this.lineFeed();
      } else {
        this.cursor.x = Math.max(0, this.cols - 2);
      }
    }
    const row = this.screen[this.cursor.y];
    if (!row) return;
    const cell = row[this.cursor.x];
    this.applyAttr(cell);
    cell.ch = ch;
    if (width === 2 && this.cursor.x + 1 < this.cols) {
      const next = row[this.cursor.x + 1];
      this.applyAttr(next);
      next.ch = '';
      next.wide = true;
    }
    if (this.cursor.x + width >= this.cols) {
      this.cursor.x = this.cols - 1;
      if (this.autowrap) this.wrapPending = true;
    } else {
      this.cursor.x += width;
    }
  }

  applyAttr(cell) {
    cell.fg = this.attr.fg;
    cell.bg = this.attr.bg;
    cell.bold = this.attr.bold;
    cell.dim = this.attr.dim;
    cell.italic = this.attr.italic;
    cell.underline = this.attr.underline;
    cell.strike = this.attr.strike;
    cell.reverse = this.attr.reverse;
    cell.hidden = this.attr.hidden;
  }

  lineFeed() {
    this.wrapPending = false;
    const bottom = this.scrollBottom;
    if (this.cursor.y >= bottom) {
      this.scrollUp(this.scrollTop, bottom, 1);
    } else if (this.cursor.y < this.rows - 1) {
      this.cursor.y += 1;
    }
    this.viewOffset = 0;
  }

  reverseIndex() {
    if (this.cursor.y <= this.scrollTop) {
      this.scrollDown(this.scrollTop, this.scrollBottom, 1);
    } else {
      this.cursor.y = Math.max(0, this.cursor.y - 1);
    }
  }

  scrollUp(top, bottom, count) {
    const n = Math.max(1, Math.min(count || 1, bottom - top + 1));
    for (let i = 0; i < n; i += 1) {
      const removed = this.screen.splice(top, 1)[0];
      if (top === 0 && !this.altActive && removed) {
        this.scrollback.push(removed);
        if (this.scrollback.length > this.maxScrollback) this.scrollback.splice(0, this.scrollback.length - this.maxScrollback);
      }
      this.screen.splice(bottom, 0, this.newRow());
    }
    this.cursor.y = Math.min(this.cursor.y, bottom);
  }

  scrollDown(top, bottom, count) {
    const n = Math.max(1, Math.min(count || 1, bottom - top + 1));
    for (let i = 0; i < n; i += 1) {
      this.screen.splice(bottom, 1);
      this.screen.splice(top, 0, this.newRow());
    }
    this.cursor.y = Math.max(this.cursor.y, top);
  }

  clearLine(row, mode) {
    const line = this.screen[row];
    if (!line) return;
    // ANSI: 0 = cursor..end, 1 = start..cursor, 2 = whole line
    const from = mode === 1 ? 0 : mode === 2 ? 0 : this.cursor.x;
    const to = mode === 0 ? this.cols - 1 : mode === 2 ? this.cols - 1 : this.cursor.x;
    for (let x = from; x <= to; x += 1) line[x] = blankCell();
  }

  clearDisplay(mode) {
    if (mode === 2 || mode === 3) {
      this.screen = this.newScreen(this.rows);
      if (mode === 3) this.scrollback = [];
      return;
    }
    if (mode === 0) {
      this.clearLine(this.cursor.y, 0);
      for (let y = this.cursor.y + 1; y < this.rows; y += 1) this.screen[y] = this.newRow();
    } else if (mode === 1) {
      this.clearLine(this.cursor.y, 1);
      for (let y = 0; y < this.cursor.y; y += 1) this.screen[y] = this.newRow();
    }
  }

  insertCells(count) {
    const line = this.screen[this.cursor.y];
    if (!line) return;
    for (let i = 0; i < count; i += 1) {
      line.splice(this.cursor.x, 0, blankCell());
      line.length = this.cols;
    }
  }

  deleteCells(count) {
    const line = this.screen[this.cursor.y];
    if (!line) return;
    for (let i = 0; i < count; i += 1) {
      line.splice(this.cursor.x, 1);
      line.push(blankCell());
    }
  }

  eraseCells(count) {
    const line = this.screen[this.cursor.y];
    if (!line) return;
    for (let i = 0; i < count && this.cursor.x + i < this.cols; i += 1) line[this.cursor.x + i] = blankCell();
  }

  insertLines(count) {
    if (this.cursor.y < this.scrollTop || this.cursor.y > this.scrollBottom) return;
    const bottom = this.scrollBottom;
    for (let i = 0; i < count; i += 1) {
      this.screen.splice(bottom, 1);
      this.screen.splice(this.cursor.y, 0, this.newRow());
    }
    this.cursor.x = 0;
  }

  deleteLines(count) {
    if (this.cursor.y < this.scrollTop || this.cursor.y > this.scrollBottom) return;
    const bottom = this.scrollBottom;
    for (let i = 0; i < count; i += 1) {
      this.screen.splice(this.cursor.y, 1);
      this.screen.splice(bottom, 0, this.newRow());
    }
    this.cursor.x = 0;
  }

  saveCursor() {
    this.savedCursor = { x: this.cursor.x, y: this.cursor.y, attr: { ...this.attr } };
  }

  restoreCursor() {
    if (!this.savedCursor) return;
    this.cursor.x = Math.min(this.cols - 1, this.savedCursor.x);
    this.cursor.y = Math.min(this.rows - 1, this.savedCursor.y);
    this.attr = { ...this.savedCursor.attr };
  }

  handleEscape(ch) {
    this.state = 'ground';
    switch (ch) {
      case '[':
        this.state = 'csi';
        this.csi = '';
        break;
      case ']':
        this.state = 'osc';
        this.osc = '';
        break;
      case 'P':
      case 'X':
      case '^':
      case '_':
        this.state = 'str';
        break;
      case '(':
      case ')':
      case '*':
      case '+':
      case '-':
      case '.':
      case '/':
        this.state = 'charset';
        break;
      case '#':
        this.state = 'hash';
        break;
      case '7':
        this.saveCursor();
        break;
      case '8':
        this.restoreCursor();
        break;
      case 'D':
        this.lineFeed();
        break;
      case 'E':
        this.cursor.x = 0;
        this.lineFeed();
        break;
      case 'M':
        this.reverseIndex();
        break;
      case 'c':
        this.reset();
        break;
      case '\\':
        break;
      default:
        break;
    }
  }

  handleOSC(raw) {
    if (!raw) return;
    const index = raw.indexOf(';');
    const command = index === -1 ? raw : raw.slice(0, index);
    const value = index === -1 ? '' : raw.slice(index + 1);
    if ((command === '0' || command === '2') && this.onTitle) this.onTitle(value);
  }

  handleCSI(final) {
    let raw = this.csi;
    let prefix = '';
    if (raw && '?><!'.includes(raw[0])) {
      prefix = raw[0];
      raw = raw.slice(1);
    }
    const params = raw.split(';').map((part) => {
      const value = parseInt(part.split(':')[0], 10);
      return Number.isFinite(value) ? value : 0;
    });
    const n = (idx, fallback = 1) => {
      const value = params[idx];
      return value === undefined || value === 0 ? fallback : value;
    };
    switch (final) {
      case 'm':
        this.applySGR(params);
        break;
      case 'H':
      case 'f':
        this.cursor.y = Math.max(0, Math.min(this.rows - 1, n(0) - 1));
        this.cursor.x = Math.max(0, Math.min(this.cols - 1, n(1) - 1));
        this.wrapPending = false;
        break;
      case 'A':
        this.cursor.y = Math.max(this.scrollTop, this.cursor.y - n(0));
        break;
      case 'B':
      case 'e':
        this.cursor.y = Math.min(this.scrollBottom, this.cursor.y + n(0));
        break;
      case 'C':
      case 'a':
        this.cursor.x = Math.min(this.cols - 1, this.cursor.x + n(0));
        this.wrapPending = false;
        break;
      case 'D':
        this.cursor.x = Math.max(0, this.cursor.x - n(0));
        this.wrapPending = false;
        break;
      case 'E':
        this.cursor.y = Math.min(this.scrollBottom, this.cursor.y + n(0));
        this.cursor.x = 0;
        break;
      case 'F':
        this.cursor.y = Math.max(this.scrollTop, this.cursor.y - n(0));
        this.cursor.x = 0;
        break;
      case 'G':
      case '`':
        this.cursor.x = Math.max(0, Math.min(this.cols - 1, n(0) - 1));
        break;
      case 'd':
        this.cursor.y = Math.max(0, Math.min(this.rows - 1, n(0) - 1));
        break;
      case 'J':
        this.clearDisplay(params[0] || 0);
        break;
      case 'K':
        this.clearLine(this.cursor.y, params[0] || 0);
        break;
      case 'L':
        this.insertLines(n(0));
        break;
      case 'M':
        this.deleteLines(n(0));
        break;
      case 'P':
        this.deleteCells(n(0));
        break;
      case '@':
        this.insertCells(n(0));
        break;
      case 'X':
        this.eraseCells(n(0));
        break;
      case 'S':
        this.scrollUp(this.scrollTop, this.scrollBottom, n(0));
        break;
      case 'T':
        this.scrollDown(this.scrollTop, this.scrollBottom, n(0));
        break;
      case 'r':
        this.scrollTop = Math.max(0, Math.min(this.rows - 1, n(0) - 1));
        this.scrollBottom = Math.max(this.scrollTop, Math.min(this.rows - 1, n(1, this.rows) - 1));
        this.cursor.x = 0;
        this.cursor.y = this.scrollTop;
        break;
      case 's':
        this.saveCursor();
        break;
      case 'u':
        this.restoreCursor();
        break;
      case 'h':
      case 'l':
        this.setModes(params, prefix, final === 'h');
        break;
      case 'n':
        if (params[0] === 6 && this.onData) this.onData(`\x1b[${this.cursor.y + 1};${this.cursor.x + 1}R`);
        break;
      case 'c':
        if (this.onData && (params[0] === 0 || params[0] === undefined)) this.onData('\x1b[?6c');
        break;
      default:
        break;
    }
  }

  setModes(params, prefix, enabled) {
    if (prefix !== '?') return;
    for (const value of params) {
      switch (value) {
        case 25:
          this.cursorVisible = enabled;
          break;
        case 7:
          this.autowrap = enabled;
          break;
        case 47:
        case 1047:
        case 1049:
          this.setAltScreen(enabled);
          break;
        case 1048:
          if (enabled) this.saveCursor();
          else this.restoreCursor();
          break;
        default:
          break;
      }
    }
  }

  setAltScreen(enabled) {
    if (enabled === this.altActive) return;
    if (enabled) {
      this.mainState = {
        screen: this.screen,
        cursor: { ...this.cursor },
        scrollTop: this.scrollTop,
        scrollBottom: this.scrollBottom,
        savedCursor: this.savedCursor ? { ...this.savedCursor } : null,
      };
      this.altActive = true;
      this.screen = this.newScreen(this.rows);
      this.cursor = { x: 0, y: 0 };
      this.scrollTop = 0;
      this.scrollBottom = this.rows - 1;
      this.wrapPending = false;
    } else if (this.mainState) {
      this.mainState.screen = this.fitRows(this.mainState.screen);
      this.screen = this.mainState.screen;
      this.cursor = {
        x: Math.min(this.cols - 1, this.mainState.cursor.x),
        y: Math.min(this.rows - 1, this.mainState.cursor.y),
      };
      this.scrollTop = Math.min(this.mainState.scrollTop, this.rows - 1);
      this.scrollBottom = Math.min(this.mainState.scrollBottom, this.rows - 1);
      this.savedCursor = this.mainState.savedCursor;
      this.altActive = false;
      this.mainState = null;
      this.wrapPending = false;
    }
  }

  fitRows(lines) {
    const next = lines.slice(0, this.rows);
    while (next.length < this.rows) next.push(this.newRow());
    return next.map((row) => {
      if (row.length === this.cols) return row;
      const copy = row.slice(0, this.cols);
      while (copy.length < this.cols) copy.push(blankCell());
      return copy;
    });
  }

  applySGR(params) {
    if (!params.length) params = [0];
    for (let i = 0; i < params.length; i += 1) {
      const value = params[i];
      if (value === 0) {
        this.attr = defaultAttr();
      } else if (value === 1) this.attr.bold = true;
      else if (value === 2) this.attr.dim = true;
      else if (value === 3) this.attr.italic = true;
      else if (value === 4) this.attr.underline = true;
      else if (value === 5 || value === 6) continue;
      else if (value === 7) this.attr.reverse = true;
      else if (value === 8) this.attr.hidden = true;
      else if (value === 9) this.attr.strike = true;
      else if (value === 21 || value === 22) { this.attr.bold = false; this.attr.dim = false; }
      else if (value === 23) this.attr.italic = false;
      else if (value === 24) this.attr.underline = false;
      else if (value === 25) continue;
      else if (value === 27) this.attr.reverse = false;
      else if (value === 28) this.attr.hidden = false;
      else if (value === 29) this.attr.strike = false;
      else if (value >= 30 && value <= 37) this.attr.fg = PALETTE[value - 30];
      else if (value === 39) this.attr.fg = null;
      else if (value >= 40 && value <= 47) this.attr.bg = PALETTE[value - 40];
      else if (value === 49) this.attr.bg = null;
      else if (value >= 90 && value <= 97) this.attr.fg = PALETTE[value - 90 + 8];
      else if (value >= 100 && value <= 107) this.attr.bg = PALETTE[value - 100 + 8];
      else if (value === 38 || value === 48) {
        const target = value === 38 ? 'fg' : 'bg';
        const mode = params[i + 1];
        if (mode === 5) {
          this.attr[target] = color256(params[i + 2] || 0);
          i += 2;
        } else if (mode === 2) {
          const r = Math.max(0, Math.min(255, params[i + 2] || 0));
          const g = Math.max(0, Math.min(255, params[i + 3] || 0));
          const b = Math.max(0, Math.min(255, params[i + 4] || 0));
          this.attr[target] = `rgb(${r},${g},${b})`;
          i += 4;
        }
      }
    }
  }

  reset() {
    this.screen = this.newScreen(this.rows);
    this.cursor = { x: 0, y: 0 };
    this.attr = defaultAttr();
    this.scrollTop = 0;
    this.scrollBottom = this.rows - 1;
    this.wrapPending = false;
    this.viewOffset = 0;
  }

  clear() {
    this.screen = this.newScreen(this.rows);
    this.cursor = { x: 0, y: 0 };
    this.scrollTop = 0;
    this.scrollBottom = this.rows - 1;
    this.wrapPending = false;
    this.viewOffset = 0;
    this.schedule();
  }

  resize(cols, rows) {
    const nextCols = Math.max(2, Math.floor(cols));
    const nextRows = Math.max(2, Math.floor(rows));
    if (nextCols === this.cols && nextRows === this.rows) return;
    const oldRows = this.rows;
    this.cols = nextCols;
    this.rows = nextRows;

    const resizeRow = (row) => {
      const copy = row.slice(0, nextCols);
      while (copy.length < nextCols) copy.push(blankCell());
      return copy;
    };
    const resized = this.screen.map(resizeRow);
    if (nextRows < oldRows && this.scrollTop === 0 && !this.altActive) {
      const removed = resized.splice(0, oldRows - nextRows);
      for (const row of removed) {
        this.scrollback.push(row);
      }
      if (this.scrollback.length > this.maxScrollback) this.scrollback.splice(0, this.scrollback.length - this.maxScrollback);
    }
    while (resized.length < nextRows) resized.push(this.newRow());
    this.screen = resized.slice(0, nextRows);
    this.scrollTop = 0;
    this.scrollBottom = nextRows - 1;
    this.cursor.x = Math.min(this.cursor.x, nextCols - 1);
    this.cursor.y = Math.min(this.cursor.y, nextRows - 1);
    this.wrapPending = false;
    this.viewOffset = Math.max(0, Math.min(this.viewOffset, this.scrollback.length));
    if (this.onResize) this.onResize(nextCols, nextRows);
    this.schedule();
  }

  fit(element, { minCols = 20, minRows = 5 } = {}) {
    if (!element) return;
    const style = window.getComputedStyle(element);
    const fontSize = parseFloat(style.fontSize) || 14;
    const lineHeight = parseFloat(style.lineHeight) || fontSize * 1.25;
    const padX = (parseFloat(style.paddingLeft) || 0) + (parseFloat(style.paddingRight) || 0);
    const padY = (parseFloat(style.paddingTop) || 0) + (parseFloat(style.paddingBottom) || 0);
    const width = Math.max(0, element.clientWidth - padX);
    const height = Math.max(0, element.clientHeight - padY);
    if (!this.measureCanvas) this.measureCanvas = document.createElement('canvas');
    const ctx = this.measureCanvas.getContext('2d');
    ctx.font = `${style.fontWeight || 400} ${fontSize}px ${style.fontFamily || 'monospace'}`;
    const charWidth = Math.max(4, ctx.measureText('MMMMMMMMMM').width / 10);
    const cols = Math.max(minCols, Math.floor(width / charWidth));
    const rows = Math.max(minRows, Math.floor(height / lineHeight));
    this.resize(cols, rows);
  }

  scrollLines(delta) {
    const max = Math.max(0, this.scrollback.length);
    this.viewOffset = Math.max(0, Math.min(max, this.viewOffset + delta));
    this.schedule();
  }

  scrollToBottom() {
    if (this.viewOffset === 0) return;
    this.viewOffset = 0;
    this.schedule();
  }

  schedule() {
    if (this.frame) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = 0;
      this.render();
    });
  }

  styleFor(cell, isCursor) {
    let fg = cell.fg || DEFAULT_FG;
    let bg = cell.bg || null;
    if (cell.reverse) {
      const tmp = fg;
      fg = bg || DEFAULT_FG;
      bg = tmp || DEFAULT_BG;
    }
    if (cell.hidden) fg = bg || DEFAULT_BG;
    if (isCursor) {
      fg = bg || DEFAULT_BG;
      bg = cell.fg || DEFAULT_FG;
    }
    return { fg, bg };
  }

  render() {
    const lines = this.scrollback.concat(this.screen);
    const total = lines.length;
    const maxStart = Math.max(0, total - this.rows);
    const start = Math.max(0, Math.min(maxStart, total - this.rows - this.viewOffset));
    const cursorAbs = this.scrollback.length + this.cursor.y;
    const fragment = document.createDocumentFragment();

    for (let r = 0; r < this.rows; r += 1) {
      const lineEl = document.createElement('div');
      lineEl.className = 'term-line';
      const row = lines[start + r];
      if (!row) {
        lineEl.textContent = ' ';
        fragment.append(lineEl);
        continue;
      }
      const cursorRow = start + r === cursorAbs && this.cursorVisible;
      let spans = [];
      let run = null;
      const flush = () => {
        if (!run) return;
        const span = document.createElement('span');
        span.className = run.className;
        span.textContent = run.text || ' ';
        if (run.fg) span.style.color = run.fg;
        if (run.bg) span.style.backgroundColor = run.bg;
        spans.push(span);
        run = null;
      };
      for (let x = 0; x < this.cols; x += 1) {
        const cell = row[x] || blankCell();
        const isCursor = cursorRow && x === this.cursor.x;
        if (!isCursor && !cell.bg && !cell.reverse && cell.ch === ' ' && x > 0) {
          const rest = row.slice(x);
          if (rest.every((c) => !c || (c.ch === ' ' && !c.bg && !c.reverse && !c.hidden))) {
            if (isCursor) break;
            continue;
          }
        }
        const colors = this.styleFor(cell, isCursor);
        const classes = [];
        if (cell.bold) classes.push('bold');
        if (cell.dim) classes.push('dim');
        if (cell.italic) classes.push('italic');
        if (cell.underline) classes.push('underline');
        if (cell.strike) classes.push('strike');
        if (isCursor) classes.push('term-cursor');
        const key = `${colors.fg}|${colors.bg || ''}|${classes.join(' ')}`;
        if (run && run.key === key) {
          run.text += cell.ch || ' ';
        } else {
          flush();
          run = { key, fg: colors.fg, bg: colors.bg, className: classes.join(' '), text: cell.ch || ' ' };
        }
      }
      flush();
      if (!spans.length) lineEl.textContent = ' ';
      else lineEl.append(...spans);
      fragment.append(lineEl);
    }
    this.element.replaceChildren(fragment);
  }

  dispose() {
    if (this.frame) cancelAnimationFrame(this.frame);
    this.frame = 0;
    if (this.element && this.element.parentNode) this.element.parentNode.removeChild(this.element);
  }
}

export function textEncoder() {
  return new TextEncoder();
}
