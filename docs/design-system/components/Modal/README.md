# Modal

A modal dialog (kit.tsx `Modal`): a native `<dialog>` in the `pop` look, `size-modal` wide (92vw on a phone), `space-modal` padding, over a 50% black backdrop.

- Header row, 28px tall, `space-modal-head` above the body: an optional Back button (`hs-back`) for a dialog's second view, the title in `modal-title` (Bricolage Grotesque 16/24 700), and the × close (`hs-x`).
- Escape, the × or a click on the backdrop closes it. Focus stays on the dialog when it opens, not on the ×.
- The title names the task in two or three words ("Rename take", "New sequence"); the body holds only what the task needs.

The consumer provides `open`, `onClose`, `title`, an optional `back` and `width`, and the body. The body's buttons shown are a sketch, not the app's exact footer.
