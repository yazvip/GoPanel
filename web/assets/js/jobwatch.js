const EVENTS = ['snapshot', 'update', 'progress', 'log', 'done', 'error'];

export function watchJob(id, { onSnapshot, onProgress, onLog, onDone, onError } = {}) {
  if (!id) return () => {};
  const source = new EventSource(`/api/v1/jobs/${encodeURIComponent(id)}/events`);
  let closed = false;

  const close = () => {
    if (closed) return;
    closed = true;
    source.close();
  };

  const parse = (ev) => {
    try {
      return JSON.parse(ev.data || '{}');
    } catch {
      return {};
    }
  };

  for (const name of EVENTS) {
    source.addEventListener(name, (ev) => {
      const data = parse(ev);
      if (name === 'snapshot') {
        if (onSnapshot) onSnapshot(data);
        if (data.status === 'success' || data.status === 'failed') {
          if (onDone) onDone(data);
          close();
        }
        return;
      }
      if (name === 'update') {
        if (data.line && onLog) onLog(data.line);
        if (data.line === undefined && onProgress
          && (data.progress !== undefined || data.message !== undefined)) onProgress(data);
        return;
      }
      if (name === 'progress') {
        if (onProgress) onProgress(data);
        return;
      }
      if (name === 'log') {
        if (onLog) onLog(data.line || '');
        return;
      }
      if (name === 'done') {
        if (onDone) onDone(data);
        close();
        return;
      }
      if (name === 'error') {
        if (onError) onError(data);
        close();
      }
    });
  }

  source.onerror = () => {
    if (closed) return;
    close();
    if (onError) onError({ error: 'connection lost' });
  };

  return close;
}

export function jobProgressModal(title) {
  const bar = document.createElement('div');
  bar.className = 'progress-inline big';
  const fill = document.createElement('span');
  fill.style.width = '0%';
  bar.append(fill);
  const label = document.createElement('div');
  label.className = 'muted small';
  const log = document.createElement('pre');
  log.className = 'log-view small-log';
  const box = document.createElement('div');
  box.append(bar, label, log);
  return {
    element: box,
    setProgress(progress, message) {
      const value = Math.max(0, Math.min(100, Number(progress) || 0));
      fill.style.width = `${value}%`;
      label.textContent = message || `${value}%`;
    },
    appendLog(line) {
      if (!line) return;
      log.textContent += line.endsWith('\n') ? line : `${line}\n`;
      log.scrollTop = log.scrollHeight;
    },
    setError(message) {
      label.textContent = message;
      fill.classList.add('failed');
    },
  };
}
