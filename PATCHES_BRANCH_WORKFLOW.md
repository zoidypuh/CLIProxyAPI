# Patches Branch Workflow

This fork uses two long-lived branches with distinct roles:

- `main`: exact mirror of `upstream/main`
- `patches`: all fork-specific changes rebased on top of `main`

## Branch roles

Use `main` only as the clean upstream mirror. Do not commit local changes there.

Use `patches` for every fork-specific fix, patch, and local runtime behavior change.

## One-time publish

Push the new branch layout to GitHub:

```bash
git push --force-with-lease origin main
git push -u origin patches
git push origin --delete codex/cache-first-user-reminder
```

The `main` push needs `--force-with-lease` because the old fork `main` contained local patch commits and is now being reset to the upstream mirror.

## Upstream sync

When upstream changes:

```bash
git fetch upstream origin --prune
git branch -f main upstream/main
git switch patches
git rebase main
```

If the rebase hits conflicts, resolve them on `patches`, then continue:

```bash
git add <resolved-files>
git rebase --continue
```

After the rebase finishes:

```bash
git push --force-with-lease origin main
git push --force-with-lease origin patches
```

## New fork changes

Start new work from `patches`, not from `main`:

```bash
git switch patches
git pull --ff-only origin patches
git switch -c feature/<name>
```

After the feature is ready, merge or rebase it back into `patches`.

## Local runtime clones

Clone local runtime copies from `patches`:

```bash
git clone -b patches https://github.com/zoidypuh/CLIProxyAPI.git
```

If a runtime checkout already exists and should track the patched branch:

```bash
git fetch origin --prune
git switch patches
git branch --set-upstream-to=origin/patches patches
git pull --ff-only origin patches
```

If the local runtime clone has no local commits and should be hard-aligned to the published patched branch:

```bash
git fetch origin --prune
git switch patches
git reset --hard origin/patches
```
