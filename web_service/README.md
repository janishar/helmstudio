# web_service

ORA Catalog Agent, the website: Next.js 16 (App Router, TypeScript), Tailwind 4 + shadcn/ui (Radix, copied into
`components/ui/`), lucide icons. Design: [docs/reference/ui/](../docs/reference/ui/);
tokens in [styles/tokens.css](styles/tokens.css) (light/dark: system preference + manual toggle).

## How it talks to the platform

Everything goes through **Kong** (`KONG_URL`), always with this app's own client key (`WEB_API_KEY`, server-side only):

| Browser | web_service | Kong |
|---|---|---|
| sign-in / sign-out forms (sign-up closed for the beta) | server actions (`app/(auth)/actions.ts`) set/clear httpOnly cookies `ora_at` (access) + `ora_rt` (refresh); forward the browser's IP (`X-Forwarded-For`) so rate limits count the real client | `/auth/signin`, `/auth/signup`, `/auth/signout` |
| any page or `/api/*` call | `proxy.ts`: no session → `/signin?next=…`; access token expiring → one shared `/auth/refresh` | `/auth/refresh` |
| `/api/v1/*`, `/api/auth/{me,password}` | `app/api/[...path]/route.ts`: reads the request body whole, streams the response (zips, SSE), adding key + bearer | `/v1/*`, `/auth/*` |
| `/files/*` (signed links) | `app/files/[...path]/route.ts`, only when the app is reached directly; behind Kong `/files` goes to files_service | `/files/*` |

Typed client: `gen/api.ts` from `contracts/openapi.yaml` (`make gen`, committed) + `openapi-fetch` (`lib/api.ts`).
Pages: `/` Catalog Agent on the newest session and `/sessions/<id>` on that one (`components/catalog/`; old `/?s=<id>` links redirect), `/dashboard` (sessions with SKU and
image counts), `/account` (catalog totals from `/v1/catalog/stats`, "Quality by product" from `/v1/catalog/metrics`, change password), and with `DEV_VIEWS=true` `/dev/jobs` (the
signed-in user's own jobs from `/v1/catalog/jobs`; one job's LLM cost, image credits, calls, tokens and models table, its nodes, a run's SKUs and images with every try's failed
goals, and its raw events; "Jobs (dev)" in the account menus for users with the dev or admin role; a 404 when the
flag is off), and for the admin `/admin/users` ("Users (admin)" in the account menus: add an account with email,
password, name and role (Beta tester or Developer), change its role, disable it or set a new password; the
`ADMIN_EMAIL` account is read-only). Sign-up is closed for the beta: `/signup` is a 404 and the sign-in page says
access is by invitation. A Beta badge sits by the product name (sign-in, header, rail). The BFF passes the admin's
`/auth/admin/users` routes and blocks the rest of `/auth/admin/*`. The Next step card shows the sample SKU's raw-photo thumbnail on each "Run sample" option and ends with an "Or run any
SKU…" menu (grouped by product, each SKU with its thumbnail, status and quote) that runs the one picked (`scope: skus`). In the agent's step rail, New session (the open one stays in Sessions) and Start Over (it empties the open
session in place, after a confirmation) sit above the account menu (the avatar at the bottom
opens Dashboard, Account and Sign out). Processing shows a raw-photo thumbnail per SKU in the SKU list (it opens a
raw photos panel with a link to the SKU's images in the Gallery), a Compare button on each image (and "Compare with raw" or `c` in the viewer) that shows the
image beside one of the SKU's raw photos, picked in the viewer's raw strip, raw-photo strips labelled with each photo's view
(`Sku.raw_views`), and a note on a SKU whose main image hasn't passed review (the error prefix `GATED`, kept in step
with agents' `run.GATED` / `run.UNREVIEWED`). The models and backgrounds pickers and the SKU edit drawer share one "Describe your model / background" box
(`components/catalog/kit.tsx` `DescribeBox`): a description alone is a valid choice ("Use described background"), and
the SKU list labels it "Described" or "Aisha + described" (`choiceLabel`). The models picker has an Ethnicity tab
between Favorites and Full library: 13 look tiles (`LOOKS` in `lib/catalog.ts`, fixed gender-neutral texts); a tile is
a described-only model whose description is its text (the box shows it, the brand may add to it; picking a photo
drops the look). Sketches: `public/catalog/looks/<id>.webp`, men's `looks/men/<id>.webp` once a session's SKUs are all
men's wear (`attributes.wearer`); a missing image leaves a plain tile. The models picker shows only the models of
the session's wears (`wearersOf`, `suitsWearers`; unknown counts as women's) and every upload; the SKU drawer has a
Women's wear / Men's wear toggle for garments and jewellery (`SkuPatch.wearer`) and lists that wear's models. An "i" `InfoTip` (kit.tsx) shows what a
subcategory is (`subcategoryTip`, `kindTip`: registry `about` and `styling`) next to a kind question's options and the
drawer's subcategory select. In the image viewer's feedback thread, a try shown big that is not the image offers "Use
this one" (`pickableTry`, `POST /images/{image_id}/pick`); "Best picked by you" marks the brand's pick (`pickedByBrand`). A generating shot whose tries are arriving shows its newest try with where it
stands (`liveTry`: "Try 2 of 3 · reviewing…", "Rejected — <reason> · trying again (3 of 3)"), keeps Stop and opens the
viewer. The Shots step's "Describe your shots" box (the same `DescribeBox`, under the Shots library grid and in each
product's brief box) sets the exact shots and their number (`exact` suggestion); the brief panel says how a brief,
links and images are used; custom shots join the library's poses for the session (`withCustomPoses`) and the step
detail tags each shot Library, Custom or "+ your reference image" (`shotTags`). Pose pictures are the library's `example_url`s, served from
`public/catalog/` (`pose-examples/`, `ora-models/`). The agent follows `GET /v1/catalog/sessions/{id}/events` (SSE) through the BFF and falls
back to polling with `?since=` when the stream can't be opened.

## Run

```sh
npm ci
cp .env.example .env.local            # KONG_URL, WEB_API_KEY (the stack's key: deploy/secrets/web_api_key)
make dev                              # http://127.0.0.1:3100
make mock                             # (other shell) Prism mock of the contract on :4010 — set KONG_URL=http://127.0.0.1:4010
```

## Test

```sh
make test        # lint + typecheck + Vitest (the gate without a stack)
```

On the stack: `docker compose -f deploy/docker-compose.yml up -d --build web` (http://localhost:8100). Kong routes `/`
to `web:3000` with the API-key plugin bypassed (the app adds its own key server-side); `WEB_API_KEY` comes from
`deploy/secrets/` (`WEB_API_KEY_FILE`) there.
