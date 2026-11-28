# FolderStrip

The Models page's Folders strip (ModelFolders.dc.html frames 1 and 3, `components/models/folders.tsx` FolderStrip): one host's model folders in priority order, each a chip with its place, name, path and free space; the folder downloads go to carries the Downloads tag.

- Strip (`fold`): `s1`, 18px 18px 18px 8px corners, 8px gap; "Folders · <host>" as a heading, then the chips, Edit… (admins) and an Info on the right.
- Folder chip (`fr`): 34px pill on `s3`, 13px/500; its place in a 22px `s5` circle (`no`), the path and free space in 12px mono `t2`.
- Not connected (`fr off`): transparent with a 1px `ln` ring, `t2` text, "Not connected" instead of the free space.
- Downloads tag (`dl`): 11px/500 pill, `a1` 24% over `s4`.
- Disk tag (`dk`) on a host chip: the folder a run loads it from ("Mac", "T9"), 10/16 mono on `s4`, 6px corners; only on a host with more than one folder. Beside it `also on T9`, `T9 not connected`, or `T9 not connected · using Mac` (11px `t2`).
- Right-click a chip: the folder menu (FolderRow).

Shown for the picked host, or for every host with more than one folder under All hosts. The consumer provides the folders (`ModelHost.folders`).
