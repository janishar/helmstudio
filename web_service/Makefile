.PHONY: dev test gen lint typecheck unit e2e mock build image

# local dev against .env.local (KONG_URL, WEB_API_KEY); `make mock` in another shell for a Prism mock on :4010
dev:
	npx next dev -p 3100

# the done gate without a stack: lint, typecheck, Vitest
test: lint typecheck unit

lint:
	npx eslint .

typecheck:
	npx tsc --noEmit

unit:
	npx vitest run

# end to end in Chromium on the TEST stack only (helmstudio-test, Kong 8390, fake runners; `make test` at the root
# brings it up), fresh users made by its admin; not part of `make test`. Uses the Chromium build already in
# ~/Library/Caches/ms-playwright (chromium-1248 for @playwright/test 1.64.1); E2E_BASE_URL overrides the address.
e2e:
	npx playwright test

# TypeScript API types from contracts/openapi.yaml (committed)
gen:
	npm run gen

mock:
	npm run mock

build:
	npx next build

image:
	docker build -t helmstudio/web:dev .
