.PHONY: dev test gen lint typecheck unit mock build image

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

# TypeScript API types from contracts/openapi.yaml (committed)
gen:
	npm run gen

mock:
	npm run mock

build:
	npx next build

image:
	docker build -t ora-catalog/web:dev .
