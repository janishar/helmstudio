# Echo (fake): assistant guide

## Card

Echo (fake) is the test model of a dev or test stack (workflows `echo`, `echo-tiny`, `echo-video` and the other
`echo-*`): it draws its prompt's text on a gradient, makes a still MP4, a short sound, a fake song (`echo-music`:
stereo, sections from the lyrics' tags) or sound effect (`echo-sound`, `echo-sound-video`, with Repaint, Vary and
Extend). Never offer it for real work.

## Settings that matter

- `echo.steps`: progress steps to report (1 to 200); `echo.delay_ms`: the wait per step.
- `echo.fail`: true fails the run after its last step, to test errors.

## Recipes

The `quick` recipe in model.yaml: 2 steps, no delay.
