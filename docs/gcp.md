# GCP bootstrap and cost guardrails

Prerequisites: `gcloud`, a billing account, an unused globally unique project ID, and a GitHub repository. Then:

```sh
gcloud auth login
gcloud auth application-default login
make gcp-bootstrap \
  GOOGLE_CLOUD_PROJECT=my-unique-project-id \
  BILLING_ACCOUNT=000000-000000-000000 \
  GITHUB_REPOSITORY=owner/repository
```

The script creates or selects the project, links billing when supplied, enables only the required APIs, creates the `(default)` Firestore Native database with delete protection, creates separate runtime, source-build, and deploy identities, and restricts GitHub federation to the named repository. It prints the two GitHub repository secrets required by CI: `WIF_PROVIDER` and `WIF_SERVICE_ACCOUNT`.

## Which account and project

Set `GCLOUD_ACCOUNT` in `.env` before anything else. Every target that reaches
Google Cloud refuses to run without it, and pins `gcloud` to that account and
project for the life of the command.

Nothing in this repository runs `gcloud config set`, and nothing calls `gcloud
auth application-default set-quota-project`. Both mutate state shared with
every other project on the machine; pinning environment variables per process
does the same job without the side effect. `make gcp-guard` prints exactly what
a command is about to act on, and says so when your globally active project is
something else.

One machine holds many projects. A deploy landing on the wrong one is the
failure this prevents.

## Billing capacity

A billing account can only be linked to a limited number of projects — five on
an unverified self-serve account. Linking fails with `Cloud billing quota
exceeded` once you hit it, and every API enablement fails after that, so check
capacity before promising a deployment:

```sh
gcloud billing projects list --billing-account=YOUR-BILLING-ID
```

Either unlink a project you no longer run, request an increase through the link
in the error, or create a second billing account.

Run `make deploy` for a manual release. CI runs the complete suite on pull requests and deploys `main` only after it passes.

## The dashboard

`/admin?key=…` counts the events the app emits. Two steps turn it on:

```sh
make analytics-setup
```

creates the BigQuery dataset, the log sink that copies event lines into it
(filter: `internal/analytics/sink.filter`), and the access both need: the
sink's identity may write to that dataset, and the runtime may read it and run
query jobs. Rerunning it converges on the same state, so a changed filter is
applied by running it again. `make deploy` passes the dataset name to the
service.

Then open Firestore in the console and create the document `config/admin`
with a string field `key`. The page compares every request against it, so
changing the field rotates the key without a deploy. With no document, the page
is locked.

A sink carries only what is written after it exists; there is no backfill.

Events that embed `analytics.Sensitive` carry data about people and go to a
separate dataset through `make analytics-sensitive-setup`.
`make analytics-sensitive-purge CONFIRM=yes` deletes that sink and dataset;
then delete the event types so nothing emits them again. The counts are
unaffected.

## A custom domain

```sh
make domain DOMAIN=example.com
```

It walks through all three steps and waits at each one: proving you own the
domain, mapping it onto the service, and publishing DNS records. Only the
middle step is a command — the other two happen in a browser and at a
registrar — so it opens the right page, waits, and re-checks rather than
printing a wall of instructions and exiting.

Ownership is proved in Search Console, which will say you do not have access to
the property. That is expected: click VERIFY YOUR OWNERSHIP and choose the DNS
TXT method. Verification is per Google account, not per project, so it is done
once per domain.

Stop and re-run it whenever you like. Each step checks whether it is already
done, and with no terminal attached it explains and exits instead of blocking.

Verifying the apex covers its subdomains, so verify the bare domain once even
if you only intend to serve `www`.

Replacing a domain's A and AAAA records stops whatever they point at now from
being served. The certificate is issued once the records resolve.

"Zero cost" is a design target, not a spending guarantee. Cloud Run and Firestore have free allowances, but billing is usage-based and shared limits can be exceeded. Set a small budget alert in Billing, watch usage, and keep same-region traffic where possible. Budget alerts notify; they do not cap spend.
