# DropZone

Where you can drop files, and how each shows it while something is dragged over.

- Gallery page (gallery.tsx): the whole page gets a 2px dashed `a1` outline 4px inside, 28px corners. Dropped images and videos upload as new takes.
- A reference field in Create (create/refs.tsx): a 2px dashed `a1` outline 2px outside, 8px corners. Images and dragged takes become references; files upload to the Gallery first.
- The Assistant composer (`ast .drop`): a 56px box, 16px corners, 2px dashed `a2`, the words 12px 600 in `a2`. Images attach to the message.
- Upload progress shows as the `a2` bar (Progress) wherever the field is; paste works the same as a drop.

The consumer provides the drop handler and what it accepts.
