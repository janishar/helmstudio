# Backlog — things to consider later

Notes from the owner while using the app. Not scheduled; each is picked up when its area is next worked on.

## Timeline

- **No way to delete an audio track once it is added** (noted 2026-11-16). "+ Audio track" adds A2, A3, A4… and
  there is no control to remove one, even when it is empty. Seen with four empty audio tracks on a two-still sequence.
  Where: `web_service/components/timeline/lanes.tsx` (the "+ Audio track" button); the document model allows up to
  8 audio tracks ([contracts/README.md](../contracts/README.md), "Timeline rules"). To decide when fixing: remove
  only empty tracks or also tracks with clips (with a confirm), and whether trailing empty tracks should disappear on
  their own.
