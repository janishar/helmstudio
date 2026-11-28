// The studio window's bridge (alpha.5 item 17, docs/mock/ContextMenus.dc.html frame 6): the spelling of the word under
// a right-click in a text field, for the page's own menu (components/context-menu.tsx), and its two spelling actions;
// the Models page's folders (plan 40, docs/mock/ModelFolders.dc.html): Browse… and Show / Open in Finder, on this Mac's
// host. Nothing else crosses: no Node, no other calls. The window's own page (ui/setup.html: the studio stopped
// answering, the page didn't load, a restart's start list) gets the setup page's calls instead, only as a file: page
const { contextBridge, ipcRenderer } = require('electron');

if (location.protocol === 'file:') {
  contextBridge.exposeInMainWorld('helm', {
    onUpdate: (cb) => ipcRenderer.on('update', (_e, snap) => cb(snap)),
    hello: () => ipcRenderer.invoke('hello'),
    answer: (kind, value) => ipcRenderer.send('answer', kind, value),
    chooseFolder: (current) => ipcRenderer.invoke('choose-folder', current),
    showLog: () => ipcRenderer.send('show-log'),
    retry: () => ipcRenderer.send('retry'),
    act: (name) => ipcRenderer.send('act', String(name)),
  });
}

contextBridge.exposeInMainWorld('helmDesktop', {
  onSpelling: (cb) => ipcRenderer.on('spelling', (_e, s) => cb(s)),
  replaceMisspelling: (word) => ipcRenderer.send('replace-misspelling', String(word)),
  addToDictionary: (word) => ipcRenderer.send('add-to-dictionary', String(word)),
  // the native runner's host name (src/stack.js RUNNER_HOST)
  localHost: 'mac',
  chooseFolder: (current) => ipcRenderer.invoke('studio-choose-folder', current == null ? undefined : String(current)),
  reveal: (p) => ipcRenderer.invoke('studio-reveal', String(p)),
});
