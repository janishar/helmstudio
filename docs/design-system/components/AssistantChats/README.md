# AssistantChats

The Assistant's chats list (assistant/assistant.tsx `ChatsList`, `chats`): a menu over the thread, under the header, opened by the Chats button.

- Box: 60px from the drawer's top, 12px in from each side, `s3`, 18 / 8 corners, 6px padding, a 1px border, scrolls when long.
- Head `chh`: "Chats" 12px 600 `t2` and "newest first" in `mono` 10px.
- New chat `new`: 36px, `s4`, 10px corners, 13/500 with a plus.
- A chat: a 44px `menuitemradio`, 10px corners; hover and the open one are `s5`. Its title 13/500 on one line, its time `mono` 10px `t2`. The open one ends with "✓ Open" in `a1` 11px; one waiting for you shows a `chw` pill (18px, amber ring and text, 10px 600).
- "Show older" is the plain `un` button (24px, `t2`, 11/500).

Arrow keys, Home and End move; Escape closes and returns to the button. The consumer provides the chats and which is open. Each chat row has its right-click menu (ContextMenu).
