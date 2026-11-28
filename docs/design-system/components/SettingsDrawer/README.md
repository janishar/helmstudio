# SettingsDrawer

Create's settings drawer, the Inspector (create.tsx `#create-settings`): every setting of the workflow, opened by the All settings chip; it remembers whether it was open.

- An `aside` with basis `size-drawer`, `space-drawer-x` side padding, `space-drawer-top` / `space-drawer-bottom`, groups `space-drawer-gap` apart; 14px from the page edges.
- Each group: a `heading` and its controls: inspector tiles (`set`), choices (`opt`), sliders, fields.
- On a window 960px and up it sits beside the composer and scrolls on its own; narrower it wraps under it.

The consumer provides the groups and their fields. The fields shown are example content.
