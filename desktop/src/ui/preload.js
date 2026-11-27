// The setup and update windows only (never the studio window, which has no preload): the few calls their pages need
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('helm', {
  onUpdate: (cb) => ipcRenderer.on('update', (_e, snap) => cb(snap)),
  hello: () => ipcRenderer.invoke('hello'),
  answer: (kind, value) => ipcRenderer.send('answer', kind, value),
  chooseFolder: (current) => ipcRenderer.invoke('choose-folder', current),
  showLog: () => ipcRenderer.send('show-log'),
  retry: () => ipcRenderer.send('retry'),
  // the error states' buttons (board Desktop.dc.html "Error states"): restart | quit | reload | restart-runner |
  // log-folder | runner-log | open-storage
  act: (name) => ipcRenderer.send('act', String(name)),
  // the update window: its state, and install | cancel | restart | recheck | notes | close
  onUpd: (cb) => ipcRenderer.on('upd', (_e, u) => cb(u)),
  updHello: () => ipcRenderer.invoke('upd-hello'),
  upd: (action) => ipcRenderer.send('upd', action),
});
