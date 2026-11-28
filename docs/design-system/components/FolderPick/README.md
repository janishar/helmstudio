# FolderPick

A radio list of folders or copies in a card (ModelFolders.dc.html frames 4 and 6, `components/models/models.tsx` Pick): Download to host…'s Folder and Delete from host…'s Copy.

- Option (`po2`): a grid of radio, name (13px/600) with its path (12px mono `t2`) and a tag, and what it says at the end (12px mono `t2`); `s3`, 12px corners.
- Picked (`aria-checked`): `s4` with a 1.5px `a1` inset ring.
- Off (`aria-disabled`): transparent with a 1px `ln` ring, `t2` text, "Not connected" (or "Read-only") at the end; it can't be picked.
- Download's Folder: shown only when the host has more than one folder; the downloads folder first and picked, with the Downloads tag and its free space; Into follows the pick.
- Delete's Copy: shown only when the variant is in more than one folder; the used copy first and picked with the `used` tag; each "18.6 GB back · 62 GB free" (the space it gives back, the free space after); Both, off while a copy is unplugged. A line under it says what loads next; the button names the copy ("Delete Mac copy", "Delete both") in `bad`.

The consumer provides the folders (`ModelHost.folders`) or copies (`ModelVariantHost.copies`).
