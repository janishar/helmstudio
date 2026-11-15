# web_service

helmstudio's website and backend-for-frontend: Next.js 16 (App Router, TypeScript), Tailwind 4 with the seven themes
as CSS variables ([styles/tokens.css](styles/tokens.css), `data-theme` on `<html>`, chosen in Settings and kept in
localStorage), Inter + Bricolage Grotesque + JetBrains Mono, lucide glyphs. Design: [docs/plan/05-ui.md](../docs/plan/05-ui.md)
section 8 and the boards in [docs/mock/](../docs/mock/).

## How it talks to the platform

Everything goes through **Kong** (`KONG_URL`), always with this app's own client key (`WEB_API_KEY`, server-side only):

| Browser | web_service | Kong |
|---|---|---|
| sign-in / sign-out | server actions (`app/(auth)/actions.ts`) set/clear httpOnly cookies `helm_at` (access) + `helm_rt` (refresh); the browser's IP goes on as `X-Forwarded-For` | `/auth/signin`, `/auth/signout` |
| any page or `/api/*` call | `proxy.ts`: no session → `/signin?next=…`; access token expiring → one shared `/auth/refresh` (only a 401 signs out) | `/auth/refresh` |
| `/api/v1/*`, `/api/auth/{me,password}`, `/api/auth/admin/users…` | `app/api/[...path]/route.ts`: allow-listed paths only, adds key + bearer, streams the answer (SSE) | `/v1/*`, `/auth/*` |
| `/files/*` (signed links, an upload's signed `PUT`) | `app/files/[...path]/route.ts` when the app is reached directly; behind Kong `/files` goes to files_service | `/files/*` |

Typed client: `gen/api.ts` from `contracts/openapi.yaml` (`npm run gen`, committed) + `openapi-fetch` (`lib/api.ts`).

## Pages

- `/signin` — no public sign-up; the admin creates accounts.
- `/` **Create** (`components/create/`): the session (`?s=<id>`, else the newest; New session), the workflow from
  `GET /v1/studio/workflows`, a form derived from its node schemas (`lib/workflow.ts`: `text` params in the composer,
  the rest in the inspector, only changed values sent as `inputs`), Generate, live progress and Cancel. The session's
  events come over SSE (`/api/v1/studio/sessions/{id}/events`, `?since=` polling when the stream can't be held) and
  are folded into the runs (`lib/runs.ts`); a run that ends is fetched again. Past runs are the takes strip.
  The takes strip is the session's takes (`GET /v1/studio/sessions/{id}/takes`) plus runs still working or failed;
  a take can be starred on the stage. `?reuse=<take id>` fills the form from `POST /v1/studio/takes/{id}/reuse`;
  without it the workflow is the session's newest run's.
- **Graph** (Create's Studio | Graph switch, ⌘\\; `components/graph/`, loaded by `next/dynamic` with `@xyflow/react`
  and elkjs only when opened): the workflow as typed cards and wires (laid out by elkjs when it has no positions),
  add nodes from `GET /v1/studio/nodes` (Add node, A, double- or right-click; auto-wiring), connect by drag (invalid
  ports dim) or the inspector's Inputs, wire-drop search, params, Bypass, "Show in Studio" (writes the `studio`
  block, `lib/studio.ts`), undo/redo. The rules, auto-wiring and wire-drop are `lib/graph.ts` (tested on
  `contracts/fixtures/graphs`); edits, history and layout `lib/graph-edit.ts`. Edits are checked with
  `POST /workflows/validate` and saved with debounce by `PUT` with the version (409 reloads); a template's first edit
  copies it as "<title> (mine)". The workflow picker lists templates and one's own (Rename, Duplicate, Delete).
  A workflow with a `studio` block shows exactly what it places in Studio; one's own with problems cannot Generate.
- Image inputs (`lib/refs.ts`, `components/create/refs.tsx`): a workflow's `image` / Image-rows param is the
  references row above the prompt and the inspector's References (the rows editor: gallery picker
  `components/gallery/picker.tsx`, upload / drop / paste, drag or Alt + arrows to reorder, `<image{n}>` labels); a drop on
  the stage adds one; sent as `{take_id}`. `?use=<take>&w=<workflow>` (and the strip's "Use in…") call
  `POST /v1/studio/takes/{id}/use`. Canvas Auto with references follows the last one (about 1 MP); a picked size sets
  `canvas.mode: fixed`.
- LoRAs (`lib/loras.ts` logic, `lib/lora-library.ts` the cached `GET /v1/studio/loras`, `components/create/loras.tsx`):
  a rows param whose item has a `Lora` field is the inspector's LoRA section and the graph card's rows (name,
  strength over the schema's range, the mode toggle, remove, drag or Alt + arrows, each row's problem), with Add LoRA
  opening the picker (docs/mock/LoraPicker.dc.html: shared + own, unfitting ones greyed with the reason, upload of a
  `.safetensors` by signed PUT with progress, the server's rejection reason, rename, delete with confirm). The graph
  rules add `lora_duplicate`, `lora_unknown`, `lora_family` from the library; takes list their LoRAs; Reuse says
  which were deleted. An empty stack is left out of the progress strip.
- `/gallery` (`components/gallery/`, `lib/takes.ts`): every take, search and filter chips, infinite scroll by
  cursor, upload (picker or drop: `POST /v1/studio/uploads` → `PUT` of the signed `/files` link with progress →
  `.../complete`), Select several (bulk star / delete), and the selected take's panel: Made with, Reuse settings,
  Star, Rename, Export file, Delete.
- `/pipelines` (`components/pipelines/`, `lib/batches.ts`): Recent batches (`GET /v1/studio/batches`, live from each
  unfinished batch's events) and New batch: a workflow, its base settings (Create's inspector), the rows as pasted
  lines, a CSV file or pasted table mapped to params, or a grid of up to 2 axes, seeds per row, Submit (a 422 that
  names a row is shown against it). `/pipelines/batches/[id]`: the batch run view, header with Pause / Resume / Stop
  and one line per row (state, takes opening full screen, Retry), live over `.../events`, rows paged by cursor.
- `/timeline`, `/models` — placeholders until their milestone.
- `/admin` (admins only in the dock) — Users: add, roles (`user`, `dev`), reset password, disable/enable; the
  `ADMIN_EMAIL` account is locked.

## Run

```sh
npm ci
cp .env.example .env.local            # KONG_URL, WEB_API_KEY (the stack's key)
make dev                              # http://127.0.0.1:3100
make mock                             # (other shell) Prism mock of the contract on :4010 — set KONG_URL=http://127.0.0.1:4010
```

## Test

```sh
make test        # lint + typecheck + Vitest (the gate without a stack)
npm run build
```

On the stack Kong routes `/` to `web:3000` with the API-key plugin bypassed (the app adds its own key server-side);
`WEB_API_KEY` comes from the `web_api_key` secret (`WEB_API_KEY_FILE`).
