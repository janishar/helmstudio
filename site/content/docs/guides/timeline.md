# Hand off to the timeline

Sequencing is not one studio's job. A sequence cut from a video studio's take, another studio's clip, a still and a voice line is none of those studios' documents, so the timeline belongs to helmstudio: studios hand it clips, and it keeps the sequence, its revisions and its exports. A studio never renders a track or runs ffmpeg itself.

@diagram the-timeline-model caption: A sequence owns no footage. Every clip points at an asset, so an edit writes a new revision and never touches a file — and a sequence using an asset counts as a reference, so nothing can offer to delete footage it is still using.

This studio holds `timeline`, `assets` and `gallery`:

@sample timeline/timeline.py

Exporting runs ffmpeg on the machine running helmstudio, found through `HELM_FFMPEG` or on the `PATH`. helmstudio does not bundle it yet.

## A sequence is a document

- **The target.** Width, height and a frame rate: 23.976, 24, 25, 29.97, 30, 48, 50, 59.94 or 60. Every time in the document lands on a whole frame of the target, and a sound's trim on a whole sample, so two editors never cut in different places.
- **The tracks.** One video track, `V1`, contiguous from zero, and up to eight audio tracks, `A1` onwards. A video clip's own sound plays under it unless it says `audio: false`. A still is held for its `hold`, and never trimmed. Clips on a track never overlap, and sound never runs past the picture.
- **Creating one.** Clips given without positions are laid end to end: video and stills on `V1`, sound on `A1`.

## Edits are revisions

`append` adds an asset to the end of a track, and needs no etag, so a studio can hand things over as it makes them. Every other edit is a merge patch against the revision you read, passed as `if_match`. Every edit keeps the document it replaced, and reverting writes an earlier revision forward as the newest one, so undoing an undo loses nothing.

Sources are never modified or copied: a clip is a reference with in and out points. An asset a sequence uses counts as used, so reclaiming disk never offers to delete footage a sequence needs.

## Editing one, from the page or the server

The host keeps every sequence and its revisions. Neither the page nor the studio's server holds one.

- **From the page**, `helm-timeline` edits a sequence. It reads it with `timeline.get`, and every edit is a `timeline.update` against the revision it read. The host checks the edit, keeps it as a new revision and answers with the document, and the component draws that answer. If the sequence changed somewhere else, the host refuses the edit, and the component reads the sequence again rather than overwrite it. What goes on a sequence is the page's choice: the component's Add sends an `add-request` event, and the page calls the element's `append` with the asset it picked.
- **From the server**, the same operations go to `HELM_API` with the token. In Python a call returns the answer, as in `helm.timeline.update(id, body, if_match=etag)`. In JavaScript it returns a promise, and options are an object, as in `await helm.timeline.update(id, body, { ifMatch: etag })`.

## Export says what it will do

`plan` says, before anything runs, whether the picture can be **copied** — every clip already matches the target and the others, and is used whole — or must be **conformed**, and names the reason for each clip that must be. A still is always drawn, so a sequence with one is conformed. The sound is always re-encoded.

An export is a job. Its progress and its cancellation are under `timeline`, so a studio need not also hold `jobs` to watch what it started. When it succeeds, the file is a gallery item labelled with the sequence it came from.

## Opening the editor

`POST /timeline/{id}:open` asks helmstudio to show a sequence in its editor. The launcher's own timeline screen is not built yet, so today it answers `501`, under helmstudio and under `helm dev` alike. A studio hides its Open button when it does; the sequence is still made, edited and exported.

## Known problems

Three things do not work as the design says, and the sample above avoids them:

- **`append` cannot add a still.** A still needs a hold, and `append` has no field for one, so it is refused. Add stills with `create`, or with `update`.
- **A dissolve short of handle is accepted.** A dissolve is centred on the cut and takes half its length from each side's material beyond the cut. It should be refused when either side has too little; today it is not.
- **A dissolve into a still exports short.** The exported file is shorter than the sequence: in the case found, four seconds of timeline exported as 3.75 seconds of video.
