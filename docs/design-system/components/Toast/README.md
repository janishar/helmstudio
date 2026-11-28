# Toast

A short status at the bottom centre after an action that leaves the screen as it was (gallery/edit.tsx `SavedToast`): `size-toast` wide (92vw on a phone), `s3`, 12px corners, `space-toast-y` / `space-toast-x` padding, 13px, `shadow-toast`, fixed 20px from the bottom, `role="status"`.

- While working: an 8px `saving` dot and "Saving…".
- Done: what happened, then the thing's name after " · " ("Saved as a new take · Fox at dusk"), and an Undo chip (28px, 12px).

The consumer provides the state and the undo handler. One toast at a time.
