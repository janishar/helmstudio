# helmstudio-next plan — Repository layout

[← Plan index](../README.md)

## 10. Repository layout

One repository, isolated services, laid out like the reference. Each service
has its own dependencies, `.env`, Dockerfile, Makefile (`dev`, `test`, `gen`,
`lint`), migrations and tests. No root virtualenv, `node_modules` or Go workspace.

```
helmstudio-next/
  contracts/          openapi.yaml · messages/*.schema.json · registry.schema.json · fixtures
  contract_tests/     black-box suite through Kong; the gate for every milestone
  orago/              shared Go framework (from the reference)
  kong/               kong.yml · apikey_auth_plugin
  auth_service/       Go
  studio_service/     Go: api/<feature>/{controller,service,dto,model,message} · messaging · dispatcher · migrations
  files_service/      Go
  pipelines_service/  Python: LangGraph graphs, consumers, migrations
  runner_service/     Python: core (graph, schema, ctx, residency, loaders, lora) · envs/mlx · envs/torch
  packs/              qwen_image/ · ltx/ · llm/   — model.yaml · torch.py / mlx.py · workflows/ · looks/ · GOTCHAS.md
  web_service/        Next.js app, BFF, generated API types
  cli/                Go: `helm` (login, run, import), an API client of studio_service
  deploy/             compose · postgres · nats · secrets · runner launch agents
  docs/               this plan
  renovate.json       dependency updates (section 18)
  tests/golden/       reference outputs recorded from the old studios
```

`make dev` brings up the compose stack with fake providers; `make runners`
starts the native runners on a GPU host.
