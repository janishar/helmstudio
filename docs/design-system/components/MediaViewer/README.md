# MediaViewer

The full-screen media viewer (media.tsx `FullScreen`): one take on black, opened with F or a take's Full screen.

- A modal dialog the size of the window on black (`#000`, backdrop black too), no padding, the take centred.
- An image keeps its own size up to the window, contained; a video fills the window, contained, plays at once in native controls; audio plays in native controls.
- Close: a 32px button 12px from the top right, 8px corners, `s3`, a 16px `x` in `t1`. Escape or a click on the black closes; ← and → step to the previous or next take.
- Opening it pauses every other video on the page, so one copy plays.

The consumer provides the take and the step handler. The picture here is a neutral stand-in.
