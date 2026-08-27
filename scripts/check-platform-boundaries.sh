#!/bin/sh
set -eu

service_dependencies=$(GOWORK=off go list -deps ./services/...)
if printf '%s\n' "$service_dependencies" | grep -q '^github.com/mss-boot-io/mss-boot-admin'; then
  echo "platform services must not import the MSS Admin runtime" >&2
  exit 1
fi

reconciler_dependencies=$(GOWORK=off go list -deps ./services/reconciler/...)
if printf '%s\n' "$reconciler_dependencies" | grep -Eq '^(database/sql|k8s\.io/)'; then
  echo "phase-one reconciler must remain a local simulation without SQL or Kubernetes drivers" >&2
  exit 1
fi

for app in apps/tenant-platform apps/mall-platform; do
  if [ -d "$app/.git" ]; then
    echo "$app must not be a nested Git repository" >&2
    exit 1
  fi
  distribution=$(cd "$app" && GOWORK=off go list -m -f '{{.Version}}' github.com/mss-boot-io/mss-boot-admin/admin)
  if [ "$distribution" != "v1.3.6" ]; then
    echo "$app uses unexpected Admin Distribution $distribution" >&2
    exit 1
  fi
  web_distribution=$(node -p "require('./$app/web/package.json').dependencies['@mss-boot-io/admin-web']")
  if [ "$web_distribution" != "1.3.6" ]; then
    echo "$app uses unexpected Admin Web $web_distribution" >&2
    exit 1
  fi
done

echo "Platform boundaries verified"
