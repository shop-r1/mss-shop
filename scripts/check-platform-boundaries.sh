#!/bin/sh
set -eu

service_dependencies=$(GOWORK=off go list -deps ./services/...)
if printf '%s\n' "$service_dependencies" | grep -q '^github.com/mss-boot-io/mss-boot-admin'; then
  echo "platform services must not import the MSS Admin runtime" >&2
  exit 1
fi

reconciler_runtime_dependencies=$(GOWORK=off go list -deps ./services/reconciler/cmd/reconciler)
if printf '%s\n' "$reconciler_runtime_dependencies" | grep -Eq '^k8s\.io/'; then
  echo "the in-cluster database reconciler must not import a Kubernetes API client" >&2
  exit 1
fi
if ! printf '%s\n' "$reconciler_runtime_dependencies" | grep -q '^github.com/jackc/pgx/v5'; then
  echo "the in-cluster reconciler must use the reviewed PostgreSQL driver" >&2
  exit 1
fi

stage_secret_dependencies=$(GOWORK=off go list -deps ./services/reconciler/cmd/stage-secrets)
if ! printf '%s\n' "$stage_secret_dependencies" | grep -q '^k8s\.io/client-go/kubernetes'; then
  echo "the trusted operator credential command must use the typed Kubernetes client" >&2
  exit 1
fi

for app in apps/tenant-platform apps/mall-platform; do
  if [ -d "$app/.git" ]; then
    echo "$app must not be a nested Git repository" >&2
    exit 1
  fi
  distribution=$(cd "$app" && GOWORK=off go list -m -f '{{.Version}}' github.com/mss-boot-io/mss-boot-admin/admin)
  if [ "$distribution" != "v1.3.7" ]; then
    echo "$app uses unexpected Admin Distribution $distribution" >&2
    exit 1
  fi
  web_distribution=$(node -p "require('./$app/web/package.json').dependencies['@mss-boot-io/admin-web']")
  if [ "$web_distribution" != "1.3.7" ]; then
    echo "$app uses unexpected Admin Web $web_distribution" >&2
    exit 1
  fi
done

echo "Platform boundaries verified"
