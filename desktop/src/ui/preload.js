// The setup window only (never the studio window, which has no preload): the few calls its page needs
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('helm', {
  onUpdate: (cb) => ipcRenderer.on('update', (_e, snap) => cb(snap)),
  hello: () => ipcRenderer.invoke('hello'),
  answer: (kind, value) => ipcRenderer.send('answer', kind, value),
  chooseFolder: (current) => ipcRenderer.invoke('choose-folder', current),
  showLog: () => ipcRenderer.send('show-log'),
  retry: () => ipcRenderer.send('retry'),
});
