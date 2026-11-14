-include .env
export

# Personal projects must not inherit an employer/private npm mirror.
NPM_CONFIG_REGISTRY := https://registry.npmjs.org/
export NPM_CONFIG_REGISTRY

APP_NAME ?= peyk
GOOGLE_CLOUD_PROJECT ?=
# The Google account that owns this project. Required by anything that reaches
# Google Cloud, so a personal deploy can never run as a work identity that
# happens to be active in the shell.
GCLOUD_ACCOUNT ?=
# Which project is billed for API quota when a local process uses your own
# credentials. Defaults to the deployment project.
QUOTA_PROJECT ?= $(GOOGLE_CLOUD_PROJECT)
# The custom domain, for `make domain`, and where its DNS is edited. {domain}
# is substituted, so the walkthrough opens the record editor for the domain in
# hand rather than a dashboard you then have to navigate. Blank it out to be
# left alone.
DOMAIN ?=
DNS_CONSOLE_URL ?= https://account.squarespace.com/domains/managed/{domain}/dns/dns-settings
GOOGLE_CLOUD_REGION ?= europe-west1
FIRESTORE_LOCATION ?= eur3
SERVICE_NAME ?= $(APP_NAME)

# Pin gcloud to this project and account for the life of a make invocation,
# instead of relying on — or mutating — the globally active configuration. One
# machine holds many projects, and `gcloud config set` is how a personal deploy
# lands on a work project. Empty values are left unset so gcloud falls back to
# its own defaults rather than being told to use nothing.
ifneq ($(GOOGLE_CLOUD_PROJECT),)
export CLOUDSDK_CORE_PROJECT := $(GOOGLE_CLOUD_PROJECT)
endif
ifneq ($(GCLOUD_ACCOUNT),)
export CLOUDSDK_CORE_ACCOUNT := $(GCLOUD_ACCOUNT)
endif
# Application Default Credentials store a quota project globally and share it
# with every other project on the machine, so `gcloud auth application-default
# set-quota-project` would repoint them all. Pinning the environment variable
# scopes it to this process, and Google's client libraries prefer it over the
# stored value.
#
# Not in CI: there the federated GitHub identity, not the deploy account, would
# be billed, and it lacks serviceusage.services.use on the project.
ifeq ($(GITHUB_ACTIONS),)
ifneq ($(QUOTA_PROJECT),)
export GOOGLE_CLOUD_QUOTA_PROJECT := $(QUOTA_PROJECT)
endif
endif
RUNTIME_SERVICE_ACCOUNT ?= $(APP_NAME)-runtime@$(GOOGLE_CLOUD_PROJECT).iam.gserviceaccount.com
BUILD_SERVICE_ACCOUNT ?= projects/$(GOOGLE_CLOUD_PROJECT)/serviceAccounts/$(APP_NAME)-build@$(GOOGLE_CLOUD_PROJECT).iam.gserviceaccount.com
TEST_PROJECT ?= peyk-test
TEST_PORT ?= 8091
export FIREBASE_EMULATORS_PATH := $(CURDIR)/.firebase/emulators
export TEST_PORT

.PHONY: help run build vendor status icons clean tidy verify test test-unit test-js test-integration test-rules \
	test-e2e test-e2e-ui test-server docker-build gcp-bootstrap deploy deploy-rules \
	smoke new gcp-guard adc-guard domain serve owner

help:
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "%-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

node_modules: package-lock.json
	npm ci
	@touch node_modules

run: ## Run locally with an ephemeral in-memory store.
	# GOOGLE_CLOUD_PROJECT is cleared deliberately. It lives in .env so cloud
	# targets can find it, and running locally must never mean writing to real
	# data because a variable happened to be set. To use Firestore on purpose:
	# GOOGLE_CLOUD_PROJECT=my-project go run ./cmd/server
	GOOGLE_CLOUD_PROJECT= go run ./cmd/server

icons: ## Redraw the favicon, app icons, and touch icon from cmd/icon.
	go run ./cmd/icon

build: ## Build the production binary.
	mkdir -p bin
	go build -trimpath -o bin/server ./cmd/server

vendor: node_modules ## Copy Firebase's browser modules into the embedded static files.
	mkdir -p internal/web/static/vendor
	for m in app auth firestore; do \
		sed -e 's#https://www.gstatic.com/firebasejs/[0-9.]*/firebase-app.js#./firebase-app.js#g' -e '/sourceMappingURL=/d' \
			node_modules/firebase/firebase-$$m.js > internal/web/static/vendor/firebase-$$m.js; \
	done

status: ## Print every live aoe session: state, last commit, push state, last reply.
	go run ./cmd/peyk status

# serve is what the relayServer aoe session runs. It builds first so the
# process that stays up is the small binary, not the Go toolchain.
serve: ## Publish session state for the phone until Ctrl-C (run in the relayServer aoe session).
	@test -n "$(GOOGLE_CLOUD_PROJECT)" || { echo "GOOGLE_CLOUD_PROJECT is required; set it in .env"; exit 1; }
	@mkdir -p bin
	@go build -trimpath -o bin/peyk ./cmd/peyk
	@bin/peyk agent

owner: gcp-guard ## Admit a Google account after its first sign-in: make owner EMAIL=you@example.com
	@test -n "$(EMAIL)" || { echo "EMAIL is required"; exit 1; }
	go run ./cmd/peyk owner $(EMAIL)

clean: ## Remove generated artifacts.
	rm -rf bin coverage.out playwright-report test-results

tidy: ## Update Go dependency metadata.
	go mod tidy

verify: ## Check formatting, modules, and static analysis without modifying files.
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))" || \
		{ echo "Go files need formatting; run gofmt -w ."; gofmt -l $$(find . -name '*.go' -not -path './.git/*'); exit 1; }
	go mod tidy
	@git diff --exit-code -- go.mod go.sum
	go vet ./...

test: verify test-unit test-js test-rules test-integration test-e2e ## Run the complete local/CI test contract.

test-unit: ## Run fast Go tests with the race detector.
	go test -race -coverprofile=coverage.out ./...

test-js: node_modules ## Test the phone app's plain modules (Markdown, board logic).
	npm run test:unit

test-rules: node_modules ## Test Firestore authorization rules in the emulator.
	npx firebase emulators:exec --only firestore --project $(TEST_PROJECT) 'npm run test:rules'

test-integration: node_modules ## Test the real Firestore adapter against the emulator.
	npx firebase emulators:exec --only firestore --project $(TEST_PROJECT) \
		'go test -race -tags=integration -count=1 ./internal/store/...'

test-server: ## Start the deterministic, credential-free E2E server.
	# Ambient cloud variables are cleared, not just the obvious one. A populated
	# .env must never change what the suite exercises, or make it billable.
	GOOGLE_CLOUD_PROJECT= QUOTA_PROJECT= FIREBASE_API_KEY= FIREBASE_AUTH_DOMAIN= FIREBASE_APP_ID= PORT=$(TEST_PORT) go run ./cmd/server

test-e2e: node_modules ## Test user journeys with Playwright.
	npx playwright install chromium
	npm run test:e2e

test-e2e-ui: node_modules ## Open Playwright's interactive debugger.
	npx playwright install chromium
	npm run test:e2e:ui

docker-build: ## Prove the production container builds.
	docker build -t $(APP_NAME):dev .

gcp-guard: ## Verify which account and project cloud commands will act on.
	@./scripts/gcp-guard.sh deploy

adc-guard: ## Verify local Application Default Credentials exist.
	@./scripts/gcp-guard.sh adc

gcp-bootstrap: gcp-guard ## Create/configure the GCP project, Firestore, IAM, APIs, and GitHub WIF.
	@test -n "$(GITHUB_REPOSITORY)" || { echo "GITHUB_REPOSITORY (owner/repo) is required"; exit 1; }
	./scripts/gcp-bootstrap.sh

deploy: gcp-guard ## Deploy the current source to a scale-to-zero Cloud Run service.
	gcloud run deploy $(SERVICE_NAME) --source . --project $(GOOGLE_CLOUD_PROJECT) --quiet \
		--region $(GOOGLE_CLOUD_REGION) --service-account $(RUNTIME_SERVICE_ACCOUNT) \
		--build-service-account $(BUILD_SERVICE_ACCOUNT) \
		--allow-unauthenticated --min-instances 0 --max-instances 3 \
		--memory 256Mi --cpu 1 --concurrency 80 --timeout 60 \
		--set-env-vars GOOGLE_CLOUD_PROJECT=$(GOOGLE_CLOUD_PROJECT),FIREBASE_API_KEY=$(FIREBASE_API_KEY),FIREBASE_AUTH_DOMAIN=$(FIREBASE_AUTH_DOMAIN),FIREBASE_APP_ID=$(FIREBASE_APP_ID)

deploy-rules: gcp-guard node_modules ## Deploy deny-by-default Firestore rules and indexes.
	npx firebase deploy --only firestore:rules,firestore:indexes --project $(GOOGLE_CLOUD_PROJECT) --force

domain: gcp-guard ## Point DOMAIN at the service and print the DNS records.
	@./scripts/domain.sh

smoke: gcp-guard ## Check the deployed health endpoint.
	./scripts/smoke.sh

new: ## Copy peyk into a sibling project: make new NAME=my-idea [DESCRIPTION='...'].
	@test -n "$(NAME)" || { echo "NAME is required"; exit 1; }
	./scripts/new-project.sh "$(NAME)" "$(DESCRIPTION)"
