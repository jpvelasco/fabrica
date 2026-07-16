# GitHub Repository Settings

These settings are configured outside the repo (GitHub UI or API) and cannot
be enforced by files in the codebase. Re-apply these when setting up a fork
or new instance.

> **Public repos:** all settings below can be applied via `gh api`.
> **Private repos on free tier:** rulesets, secret scanning, push protection,
> and CodeQL must be set via the GitHub UI.

---

## Apply via `gh api` (replace `OWNER/REPO`)

```bash
# General settings
gh api repos/OWNER/REPO \
  --method PATCH \
  --field delete_branch_on_merge=true \
  --field default_branch=main

# Dependabot
gh api repos/OWNER/REPO/vulnerability-alerts --method PUT
gh api repos/OWNER/REPO/automated-security-fixes --method PUT

# Secret scanning + push protection
gh api repos/OWNER/REPO \
  --method PATCH \
  --header "Content-Type: application/json" \
  --input - <<'EOF'
{
  "security_and_analysis": {
    "secret_scanning": { "status": "enabled" },
    "secret_scanning_push_protection": { "status": "enabled" }
  }
}
EOF

# CodeQL default setup
gh api repos/OWNER/REPO/code-scanning/default-setup \
  --method PATCH \
  --input - <<'EOF'
{ "state": "configured", "query_suite": "default" }
EOF

# Branch ruleset: protect-main
gh api repos/OWNER/REPO/rulesets \
  --method POST \
  --header "Content-Type: application/json" \
  --input - <<'EOF'
{
  "name": "protect-main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] }
  },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": true,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "do_not_enforce_on_create": false,
        "required_status_checks": [
          { "context": "Lint" },
          { "context": "Vulnerability scan" },
          { "context": "Build (ubuntu-latest)" },
          { "context": "Build (windows-latest)" },
          { "context": "Test (ubuntu-latest)" },
          { "context": "Test (windows-latest)" },
          { "context": "Release build (snapshot)" }
        ]
      }
    }
  ],
  "bypass_actors": [
    { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" }
  ]
}
EOF

# Tag ruleset: protect-version-tags
gh api repos/OWNER/REPO/rulesets \
  --method POST \
  --header "Content-Type: application/json" \
  --input - <<'EOF'
{
  "name": "protect-version-tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": {
    "ref_name": { "include": ["refs/tags/v*"], "exclude": [] }
  },
  "bypass_actors": [
    { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" }
  ],
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" }
  ]
}
EOF
```

> **Status-check names** match job `name:` values in `.github/workflows/ci.yml`.
> macOS Build/Test are **not** required: PR matrix skips macOS to save Actions
> minutes; requiring them would block every PR. They still run on push to `main`.

---

## Current settings

### General
- Default branch: `main`
- Auto-delete head branches: enabled

### Branch ruleset (`protect-main` → default branch)
- Method: **Repository rulesets** (not classic branch protection)
- Require PR before merging: yes
- Required approvals: 0
- Dismiss stale reviews on push: yes
- Require review from code owners: yes
- Require conversation resolution: yes
- Require branch up to date (strict): yes
- Block force pushes: yes (`non_fast_forward`)
- Block branch deletion: yes
- Required checks: Lint, Vulnerability scan, Build (ubuntu/windows), Test (ubuntu/windows), Release build (snapshot)
- Bypass: repository Admin role (solo-maintainer escape hatch)

### Tag ruleset (`protect-version-tags` → `v*`)
- Restrict deletions: yes
- Restrict updates: yes
- Bypass: repository Admin role

### Security & Analysis
- Secret scanning: enabled (public)
- Push protection: enabled (public)
- Dependabot alerts: enabled
- Dependabot security updates: enabled
- CodeQL: enable via default setup if desired (UI or API)
