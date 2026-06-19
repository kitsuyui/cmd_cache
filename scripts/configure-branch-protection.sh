#!/bin/bash
set -euo pipefail

repo="${GITHUB_REPOSITORY:-kitsuyui/cmd_cache}"
tmpfile=$(mktemp)
trap 'rm -f "$tmpfile"' EXIT

cat > "$tmpfile" <<'JSON'
{
  "required_status_checks": {
    "strict": false,
    "contexts": ["test"]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null
}
JSON

gh api "repos/${repo}/branches/main/protection" \
  --method PUT \
  --header "Content-Type: application/json" \
  --input "$tmpfile"
