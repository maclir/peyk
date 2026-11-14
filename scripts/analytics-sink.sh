#!/usr/bin/env bash
# Creates or updates one log sink into one BigQuery dataset, and the access
# both sides need. Safe to rerun: every step converges on the same end state.
#
#   analytics-sink.sh DATASET SINK FILTER_FILE DESCRIPTION
set -euo pipefail

: "${GOOGLE_CLOUD_PROJECT:?GOOGLE_CLOUD_PROJECT is required}"
: "${ANALYTICS_LOCATION:=${GOOGLE_CLOUD_REGION:?GOOGLE_CLOUD_REGION or ANALYTICS_LOCATION is required}}"
: "${RUNTIME_SERVICE_ACCOUNT:?RUNTIME_SERVICE_ACCOUNT is required}"
dataset=${1:?dataset}
sink=${2:?sink}
filter_file=${3:?filter file}
description=${4:?description}

project=$GOOGLE_CLOUD_PROJECT
filter=$(tr '\n' ' ' <"$filter_file")
destination="bigquery.googleapis.com/projects/$project/datasets/$dataset"

gcloud services enable cloudresourcemanager.googleapis.com bigquery.googleapis.com logging.googleapis.com --project "$project"

if ! bq show --dataset "$project:$dataset" >/dev/null 2>&1; then
  bq --location="$ANALYTICS_LOCATION" mk --dataset --description "$description" "$project:$dataset"
fi

# A sink carries only what is written after it exists, so creating it before
# the first deploy that emits the events loses nothing.
if gcloud logging sinks describe "$sink" --project "$project" >/dev/null 2>&1; then
  gcloud logging sinks update "$sink" "$destination" --project "$project" \
    --log-filter="$filter" --use-partitioned-tables
else
  gcloud logging sinks create "$sink" "$destination" --project "$project" \
    --description="$description" --log-filter="$filter" --use-partitioned-tables
fi

# The sink writes as an identity Google mints for it, which starts with no
# access. Access is granted on this dataset alone rather than the project, and
# the runtime may read it and nothing else.
writer=$(gcloud logging sinks describe "$sink" --project "$project" --format='value(writerIdentity)')
writer=${writer#serviceAccount:}
access=$(mktemp)
trap 'rm -f "$access"' EXIT
bq show --format=prettyjson --dataset "$project:$dataset" |
  jq --arg writer "$writer" --arg runtime "$RUNTIME_SERVICE_ACCOUNT" \
    '{access: (((.access // []) | map(select(.userByEmail != $writer and .userByEmail != $runtime)))
      + [{role: "WRITER", userByEmail: $writer}, {role: "READER", userByEmail: $runtime}])}' >"$access"
bq update --dataset --source="$access" "$project:$dataset" >/dev/null

# Reading a dataset is not enough to query it: the query runs as a job, and
# jobs are created at project level.
gcloud projects add-iam-policy-binding "$project" \
  --member="serviceAccount:$RUNTIME_SERVICE_ACCOUNT" --role=roles/bigquery.jobUser --condition=None >/dev/null

echo "sink $sink -> $project:$dataset"
