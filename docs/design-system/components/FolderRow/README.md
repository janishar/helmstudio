# FolderRow

A row of Edit folders (ModelFolders.dc.html frames 2 and 5, `components/models/folders.tsx` EditFolders): one model folder of a host, in a list the admin drags (or Alt+↑ ↓) to reorder. The order is the priority: a model loads from the first folder that has it.

- Row (`frow`): a grid of grip (⋮⋮, `t2`), place (22px `s5` circle), name and path (13px/600 over 11px mono `t2`, "Connected" in 11px `a1`), free space with a 4px `a1` bar of the used share, the Downloads radio, and ✕ (Remove…). `s3`, 12px corners.
- Dragged (`frow lift`): `s4`, lifted 3px, a 1px `a2` ring.
- Not connected (`frow off`): transparent with a 1px `ln` ring, name and place at 0.6, "Not connected" with a hollow dot; its Downloads radio is off.
- A path just added: "Checked on Save" in place of the space; the runner checks it on Save.
- ✕ and Remove… open the confirm: "Remove T9 from the list?", "The files stay on disk. Models only there show as missing on mac.", Cancel and a `bad` Remove. The last folder can't be removed.
- Right-click: Move up, Move down, Use for downloads, Open in Finder (the Mac app, a folder on this Mac; greyed "In the Mac app" in a browser), then Remove…; greyed items say why ("Already first", "Downloads go here", "Not connected").

Below the list: a mono path field, Browse… (the Mac app only) and Add; Cancel and Save. Admins only.
