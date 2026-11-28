// The Mac app's bridge (desktop/src/ui/studio-preload.js, `window.helmDesktop`): absent in a browser; each call only
// when that build of the app offers it.
export type Desktop = {
  /** the word under a right-click in a text field and its suggestions (components/context-menu.tsx) */
  onSpelling?: (cb: (s: { word: string; suggestions: string[] }) => void) => void;
  replaceMisspelling?: (word: string) => void;
  addToDictionary?: (word: string) => void;
  /** this Mac's runner host name */
  localHost?: string;
  /** the folder dialog and Show in Finder (components/models/folders.tsx) */
  chooseFolder?: (current?: string) => Promise<string | null>;
  reveal?: (path: string) => Promise<boolean>;
  /** this Mac's runners (components/status.tsx) */
  restartRunners?: () => void;
  restartRunner?: (env: string) => void;
  showLog?: () => void;
};

export const desktop = () => (globalThis as { helmDesktop?: Desktop }).helmDesktop;
