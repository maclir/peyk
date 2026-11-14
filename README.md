# peyk

Send tasks to your aoe coding sessions and follow their state, commits, and pushes from your phone.

A small, opinionated starting point for validating product ideas with near-zero idle infrastructure cost. It distills recurring patterns from `zynk`, `areh`, `knip`, and `dekode` into one stateless Go service, Firestore persistence, Cloud Run scale-to-zero deployment, keyless GitHub CI/CD, and a complete local test pyramid.

## Start a new idea

For deterministic generation from this repository:

```sh
make new NAME=my-idea DESCRIPTION='One sentence about the product.'
cd ../my-idea
make test
```

The generator copies only source-controlled scaffold content, rewrites the Go module and project names, initializes a fresh `main` branch, and stages the result for review. It deliberately does not create cloud resources, make a commit, or contact GitHub.

For collaborative product shaping, open Claude Code in peyk and invoke the project skill:

```text
/incubate A lightweight app that helps ...
```

The skill first discusses the user, hypothesis, scope, risks, and technical tradeoffs with you. It writes a concise incubation brief and asks for explicit approval before creating or implementing anything. After approval it uses `make new`, records the brief in the generated project, builds one walking skeleton, and runs the full test suite. See `.claude/skills/incubate/SKILL.md` for the workflow.

## Develop

```sh
cp .env.example .env
make run       # http://localhost:8080, ephemeral memory store
make test      # the complete pre-commit and CI contract
make help
```

Set `GCLOUD_ACCOUNT` in `.env` before any cloud command: every target that
reaches Google Cloud refuses to run without it, and pins gcloud to it rather
than trusting — or changing — whichever project is globally active.

Leave `GOOGLE_CLOUD_PROJECT` empty for local work; `make run` clears it anyway. Firestore adapter and security behavior are tested against the emulator; Playwright uses the real server with deterministic in-memory state. This prevents tests and agents from needing cloud credentials or touching production data.

## Provision and deploy

See `docs/gcp.md`. The short form is:

```sh
make gcp-bootstrap GOOGLE_CLOUD_PROJECT=my-unique-id \
  BILLING_ACCOUNT=000000-000000-000000 GITHUB_REPOSITORY=owner/repo
make deploy GOOGLE_CLOUD_PROJECT=my-unique-id
```

Before enabling CI deployment, replace `PROJECT_ID` in `.github/workflows/ci.yml` and add the two secrets printed by `gcp-bootstrap`.

## What was carried forward

- From all four projects: Go's small single-binary service and Cloud Run source deploys.
- From `areh` and `dekode`: a store interface with an in-memory twin, plus Playwright running against a credential-free test server.
- From `dekode`: explicit scale-to-zero/cost invariants, Firestore emulator integration tests, rules tests, deny-by-default browser access, deploy smoke tests, and heavily documented operational traps.
- From the newer CI setups: GitHub OIDC/Workload Identity Federation instead of long-lived JSON keys.
- From `zynk`: one discoverable Makefile as the operator interface and workflow documentation for agents.

Read `AGENTS.md` for the working agreement and `docs/architecture.md` for the boundaries intended to survive product evolution.
