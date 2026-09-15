# <milestone id> — implementation report

## What was built

Two or three sentences. Not a file list; the diff is the file list.

## Gate

    $ make gate
    <paste the actual output, or state plainly why it could not run>

## Design contradictions raised

Each one: the document and section, the quotation, the competing quotation,
what you would do, and whether you stopped or were told how to proceed. Write
"none" if there were none — do not omit the heading.

## Judgement calls

Every place the design did not decide for you and you decided. What you chose,
what you rejected, and why. This is the section a reviewer reads first, and the
one most likely to surface a divergence a milestone before it gets expensive.

## Could not verify

Everything machine-bound: Metal, real weights, memory behaviour, ffmpeg on
videotoolbox, Keychain, signing, notarisation. Be specific about what would
demonstrate it, so the integration window has a script to follow.

## Dependencies added

Module, version, what it does, why the standard library was not enough.
"None" if none.

## Files touched outside the milestone's list

With the reason. "None" if none.

## Decision-log entries appended

The lines you added to `docs/decisions.md`, verbatim.

## Left undone

Anything in the milestone you did not finish, and what is in the way.
