#!/usr/bin/env bash
set -euo pipefail

: "${GOOGLE_CLOUD_PROJECT:?GOOGLE_CLOUD_PROJECT is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be owner/repository}"

APP_NAME=${APP_NAME:-peyk}
GOOGLE_CLOUD_REGION=${GOOGLE_CLOUD_REGION:-europe-west1}
FIRESTORE_LOCATION=${FIRESTORE_LOCATION:-eur3}
BILLING_ACCOUNT=${BILLING_ACCOUNT:-}
WIF_POOL=${WIF_POOL:-github}
WIF_PROVIDER_ID=${WIF_PROVIDER_ID:-github}
RUNTIME_ACCOUNT=${RUNTIME_ACCOUNT:-$APP_NAME-runtime}
BUILD_ACCOUNT=${BUILD_ACCOUNT:-$APP_NAME-build}
DEPLOY_ACCOUNT=${DEPLOY_ACCOUNT:-$APP_NAME-deploy}

if ! gcloud projects describe "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud projects create "$GOOGLE_CLOUD_PROJECT" --name "$APP_NAME"
fi

if [[ -n "$BILLING_ACCOUNT" ]]; then
  gcloud billing projects link "$GOOGLE_CLOUD_PROJECT" --billing-account "$BILLING_ACCOUNT"
fi

# Deliberately no `gcloud config set project` here. The project is pinned for
# this process through CLOUDSDK_CORE_PROJECT, so bootstrapping one project does
# not repoint the shell you use for everything else.
gcloud services enable \
  artifactregistry.googleapis.com \
  cloudbuild.googleapis.com \
  firestore.googleapis.com \
  firebaserules.googleapis.com \
  iamcredentials.googleapis.com \
  run.googleapis.com \
  serviceusage.googleapis.com \
  sts.googleapis.com \
  --project "$GOOGLE_CLOUD_PROJECT"

if ! gcloud firestore databases describe --database='(default)' --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud firestore databases create \
    --database='(default)' \
    --location="$FIRESTORE_LOCATION" \
    --type=firestore-native \
    --delete-protection \
    --project "$GOOGLE_CLOUD_PROJECT"
fi

# `gcloud run deploy --source` pushes here. Creating it up front keeps the
# first deploy from stopping on an interactive prompt, which in CI is a silent
# ten minute hang rather than an error.
if ! gcloud artifacts repositories describe cloud-run-source-deploy \
  --location="$GOOGLE_CLOUD_REGION" --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud artifacts repositories create cloud-run-source-deploy \
    --repository-format=docker --location="$GOOGLE_CLOUD_REGION" \
    --description="Cloud Run source deploys" --project "$GOOGLE_CLOUD_PROJECT"
fi

runtime_email="$RUNTIME_ACCOUNT@$GOOGLE_CLOUD_PROJECT.iam.gserviceaccount.com"
build_email="$BUILD_ACCOUNT@$GOOGLE_CLOUD_PROJECT.iam.gserviceaccount.com"
deploy_email="$DEPLOY_ACCOUNT@$GOOGLE_CLOUD_PROJECT.iam.gserviceaccount.com"

if ! gcloud iam service-accounts describe "$runtime_email" --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$RUNTIME_ACCOUNT" --display-name "$APP_NAME runtime" --project "$GOOGLE_CLOUD_PROJECT"
fi
if ! gcloud iam service-accounts describe "$build_email" --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$BUILD_ACCOUNT" --display-name "$APP_NAME source build" --project "$GOOGLE_CLOUD_PROJECT"
fi
if ! gcloud iam service-accounts describe "$deploy_email" --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$DEPLOY_ACCOUNT" --display-name "$APP_NAME GitHub deploy" --project "$GOOGLE_CLOUD_PROJECT"
fi

gcloud projects add-iam-policy-binding "$GOOGLE_CLOUD_PROJECT" \
  --member="serviceAccount:$runtime_email" --role=roles/datastore.user --condition=None >/dev/null
gcloud projects add-iam-policy-binding "$GOOGLE_CLOUD_PROJECT" \
  --member="serviceAccount:$build_email" --role=roles/run.builder --condition=None >/dev/null

# `gcloud run deploy --source` does four separate things as the deploying
# identity, and each needs its own role: it reads and creates the Artifact
# Registry repository, uploads a source tarball to a run-sources-* bucket,
# submits a Cloud Build, and then updates the service. A local deploy hides all
# of it, because whoever runs one is usually a project owner. Only CI, running
# as this service account, finds out — one round trip per missing role.
for role in roles/run.admin roles/serviceusage.serviceUsageConsumer roles/firebaserules.admin \
  roles/datastore.indexAdmin roles/artifactregistry.admin roles/storage.admin \
  roles/cloudbuild.builds.editor; do
  gcloud projects add-iam-policy-binding "$GOOGLE_CLOUD_PROJECT" \
    --member="serviceAccount:$deploy_email" --role="$role" --condition=None >/dev/null
done
for target_email in "$runtime_email" "$build_email"; do
  gcloud iam service-accounts add-iam-policy-binding "$target_email" \
    --member="serviceAccount:$deploy_email" --role=roles/iam.serviceAccountUser \
    --project "$GOOGLE_CLOUD_PROJECT" --condition=None >/dev/null
done

if ! gcloud iam workload-identity-pools describe "$WIF_POOL" --location=global --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud iam workload-identity-pools create "$WIF_POOL" --location=global \
    --display-name="GitHub Actions" --project "$GOOGLE_CLOUD_PROJECT"
fi

if ! gcloud iam workload-identity-pools providers describe "$WIF_PROVIDER_ID" --workload-identity-pool "$WIF_POOL" --location=global --project "$GOOGLE_CLOUD_PROJECT" >/dev/null 2>&1; then
  gcloud iam workload-identity-pools providers create-oidc "$WIF_PROVIDER_ID" \
    --workload-identity-pool "$WIF_POOL" \
    --location=global \
    --issuer-uri=https://token.actions.githubusercontent.com \
    --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
    --attribute-condition="assertion.repository=='$GITHUB_REPOSITORY'" \
    --project "$GOOGLE_CLOUD_PROJECT"
fi

project_number=$(gcloud projects describe "$GOOGLE_CLOUD_PROJECT" --format='value(projectNumber)')
principal="principalSet://iam.googleapis.com/projects/$project_number/locations/global/workloadIdentityPools/$WIF_POOL/attribute.repository/$GITHUB_REPOSITORY"
gcloud iam service-accounts add-iam-policy-binding "$deploy_email" \
  --member="$principal" --role=roles/iam.workloadIdentityUser \
  --project "$GOOGLE_CLOUD_PROJECT" --condition=None >/dev/null

provider=$(gcloud iam workload-identity-pools providers describe "$WIF_PROVIDER_ID" \
  --workload-identity-pool "$WIF_POOL" --location=global \
  --project "$GOOGLE_CLOUD_PROJECT" --format='value(name)')

echo
echo "GCP bootstrap complete. Add these GitHub Actions repository secrets:"
echo "WIF_PROVIDER=$provider"
echo "WIF_SERVICE_ACCOUNT=$deploy_email"
