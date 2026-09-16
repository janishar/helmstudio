# Sessions

Almost every studio has a notion of a working session: the prompt you were refining, the seed you liked, the takes you made along the way. helmstudio makes that a platform concept, so no studio has to invent its own, and every studio's sessions have the same shape.

A session is a name and a `state` document that is entirely the studio's. This studio holds `kv`, `assets` and `gallery`:

@sample sessions/sessions.py

## State belongs to the studio

helmstudio never reads `state`. Put in it whatever the studio's page needs to come back to where the person left off.

## Changes are merge patches, against what you read

`update` applies a JSON merge patch: the fields you send replace those in the document, and the rest are kept. Every read returns an `etag`; pass it as `if_match`, and an update made against a document someone else has changed since — a second tab, say — is refused as a `Conflict` rather than silently overwriting theirs. Read it again, merge, and retry.

## Items made during a session say so

A gallery item recorded with a `session_id` belongs to that session, and `gallery.query(session_id=…)` finds them.

## Duplicate, rather than lose your place

`duplicate` copies a session's state under a new name, so trying something else costs nothing. Deleting a session is a soft delete: its name becomes free again, and the items made during it keep their `session_id`.
