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
| `/files/*` (signed links) | `app/files/[...path]/route.ts` when the app is reached directly; behind Kong `/files` goes to files_service | `/files/*` |

Typed client: `gen/api.ts` from `contracts/openapi.yaml` (`npm run gen`, committed) + `openapi-fetch` (`lib/api.ts`).

## Pages

- `/signin` — no public sign-up; the admin creates accounts.
- `/` **Create** (`components/create/`): the session (`?s=<id>`, else the newest; New session), the workflow from
  `GET /v1/studio/workflows`, a form derived from its node schemas (`lib/workflow.ts`: `text` params in the composer,
  the rest in the inspector, only changed values sent as `inputs`), Generate, live progress and Cancel. The session's
  events come over SSE (`/api/v1/studio/sessions/{id}/events`, `?since=` polling when the stream can't be held) and
  are folded into the runs (`lib/runs.ts`); a run that ends is fetched again. Past runs are the takes strip.
- `/pipelines`, `/gallery`, `/timeline`, `/models` — placeholders until their milestone.
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
