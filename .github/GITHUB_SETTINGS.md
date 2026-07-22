# GitHub Repository Settings

These settings are configured outside the repo (GitHub UI or API) and cannot
be enforced by files in the codebase alone. Re-apply these when setting up a
fork or new instance. Keep this file in sync when rulesets change.

> **Public repos:** all settings below can be applied via `gh api`.
> **Private repos on free tier:** rulesets, secret scanning, push protection,
> and CodeQL may need the GitHub UI depending on plan.

---

## Protection model (rulesets vs classic)

Fabrica uses **repository rulesets**, not classic branch protection.

| API | What it means |
|-----|----------------|
| `GET .../branches/main/protection` → **404** | Classic protection is **off** — **not** “main is unprotected” |
| `GET .../rulesets` | Ruleset inventory (`protect-main`, `protect-version-tags`, …) |
| `GET .../rules/branches/main` | **Ground truth** for rules applied to `main` |

```bash
# Inventory
gh api repos/OWNER/REPO/rulesets --jq '.[] | {id, name, target, enforcement}'

# What actually applies to main (required checks, PR rules, etc.)
gh api repos/OWNER/REPO/rules/branches/main \
  --jq '{
    rule_types: [.[].type] | unique,
    required_checks: [.[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context] | unique,
    merge_methods: [.[] | select(.type == "pull_request") | .parameters.allowed_merge_methods] | add
  }'

# Classic (expect 404 when rulesets-only)
gh api repos/OWNER/REPO/branches/main/protection 2>&1 | head -1
```

Agents and humans must use the ruleset endpoints when checking protection.
Do not report “branch not protected” from a classic 404 alone.

---

## Apply via `gh api` (replace `OWNER/REPO`)

```bash
# General settings — squash-only merge UI + auto-delete heads
gh api repos/OWNER/REPO \
  --method PATCH \
  --field delete_branch_on_merge=true \
  --field default_branch=main \
  --field allow_squash_merge=true \
  --field allow_merge_commit=false \
  --field allow_rebase_merge=false

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
# If the ruleset already exists, PUT to /rulesets/{id} instead of POST.
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
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["squash"]
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

### Update an existing ruleset (example: squash-only)

```bash
# List ids
gh api repos/OWNER/REPO/rulesets --jq '.[] | {id, name}'

# PUT full ruleset body (same JSON as create, with allowed_merge_methods: ["squash"])
gh api repos/OWNER/REPO/rulesets/RULESET_ID \
  --method PUT \
  --input protect-main.json
```

### Intentionally **not** required (yet)

| Check | Why not in ruleset |
|-------|--------------------|
| **Codacy Static Code Analysis** | Third-party; lag/missed webhooks can hard-block merges. Soft-gated via PR template + pr-auto. Revisit after sustained reliability. |
| gosec, Trivy, CodeQL, Lint (Windows) | Run in CI; keep advisory until we want harder gates |
| **`codecov/patch`** | Posted by the **Codecov GitHub App** after CI uploads coverage (not a standalone Actions job). Soft gate: visible on PRs, enforced by `codecov.yml` patch target, but App lag/outage must not deadlock merges. pr-auto Gate 5.7 when the status is present. |

### Codecov standard (fabrica reference)

Sister repos should match this pattern (see `.github/workflows/ci.yml` Test job):

1. **Auth (XOR):** default **OIDC** (`permissions.id-token: write` + `use_oidc: ${{ secrets.CODECOV_TOKEN == '' }}`). If repo secret `CODECOV_TOKEN` is set, **disable OIDC** and use the upload token only — `codecov-action` documents that `use_oidc: true` **ignores** any token. Do not enable both at once.
2. **Action:** `codecov/codecov-action@v7` (pin full SHA) twice:
   - coverage: `files: ./coverage.out`, `fail_ci_if_error: true`, `use_pypi: true`, `slug: OWNER/REPO`
   - test analytics: `report_type: test_results`, `files: ./junit.xml`, `fail_ci_if_error: false`
3. **Identity (required):** always set
   - `override_commit: ${{ github.event.pull_request.head.sha || github.sha }}`
   - `override_branch: ${{ github.head_ref || github.ref_name }}`
   - `override_pr: ${{ github.event.pull_request.number }}`  
   Without these, **push-to-main** uploads often omit SHA/branch (empty `CC_SHA` / `CC_BRANCH` in logs) and Codecov never stores a usable **base** report → PRs show “Missing base commit” / “Coverage data is unknown”.
4. **Checkout:** `fetch-depth: 0` on the Test job so git history/path mapping is complete.
5. **Do not** use deprecated `codecov/test-results-action`.
6. **Coverage generation:** Linux leg with `gotestsum` + `-coverprofile` (product still **builds/tests** on Windows/macOS). Multi-OS *coverage uploads* only when you have platform-tagged packages that Linux cannot exercise (juggernaut).
7. **PR UI:** `codecov/patch` + `codecov[bot]` come from the Codecov GitHub App after both **base (main)** and **head** have processable reports ([Missing base](https://docs.codecov.com/docs/error-reference#section-missing-base-report) / [Missing head](https://docs.codecov.com/docs/error-reference#section-missing-head-commit)).
8. **Dashboard:** App installed; after each `main` merge, open `https://app.codecov.io/gh/OWNER/REPO/commit/<main-sha>` and confirm a coverage **%** (not “unknown”).

---

## Current settings

### General

- Default branch: `main`
- Auto-delete head branches: enabled
- **Merge methods:** **squash only** (`allow_squash_merge=true`; merge commit and rebase disabled in repo settings)

### Branch ruleset (`protect-main` → default branch)

- Method: **Repository rulesets** (not classic branch protection)
- Require PR before merging: yes
- **Allowed merge methods: `squash` only**
- Required approvals: 0
- Dismiss stale reviews on push: yes
- Require review from code owners: yes
- Require conversation resolution: yes
- Require branch up to date (strict): yes
- Block force pushes: yes (`non_fast_forward`)
- Block branch deletion: yes
- Required checks: Lint, Vulnerability scan, Build (ubuntu/windows), Test (ubuntu/windows), Release build (snapshot)
- **Not required (yet)** but present in CI: Lint (Windows), gosec, Trivy, CodeQL (`codeql.yml`), Codacy
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
