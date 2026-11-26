# Progress

How far a long job has got.

- Rows done (`prog`): an 84 by 6px pill on `s4` with a filled bar (a pipeline batch), then a mono count.
- Upload (kit.tsx `UploadStatus`): the step text ("Optimising…" for an image, "Optimising 62%" for audio, "Uploading 48%"), a 4px bar on `in` filled `a2` while a fraction is known (`radius-track`), and a mono 10px line ("12.4 → 1.9 MB · WebP", or "as is").
- A run step (`step`): 6px 10px on `s1`, `radius-small`, 13px, an icon and the step's name.

The consumer provides the fraction and the words; give the bar `role="progressbar"` with its values and a label naming the file.
