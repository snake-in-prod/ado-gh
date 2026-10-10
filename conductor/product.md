# Initial Concept

I want to make a tool like gh that will be working with ADO (Azure Devops) working like gh but for ADO

# Product Guide

## Vision
`ado` is an open-source command-line tool for Azure DevOps that feels like GitHub's `gh`. Developers who already know `gh` should be productive right away: same command shapes, same flags, same scripting habits, aimed at Azure DevOps repos, pull requests, work items and pipelines.

## Problem
Today's Azure DevOps terminal tooling (`az devops` extension, raw REST) is verbose, needs org/project flags on every call, and doesn't match the workflows developers know from `gh`. Moving between GitHub and Azure DevOps means relearning tools.

## Target Users
- **Developers used to `gh`:** want the same muscle memory (`ado pr create`, `ado pr checkout 123`) on Azure DevOps.
- **CI/CD and scripting:** pipelines and scripts that need stable, machine-readable output and a raw API escape hatch.
- **Team leads / PMs:** triage work items, boards, sprints and PR queues from the terminal.

## Core Features
### Command groups
- **`repo`**: list, view, clone, create, browse.
- **`pr`**: create, list, view, checkout, diff, review/approve/vote, comment, merge/complete, close/abandon, status, reviewers.
- **`workitem`** (issue-like, alias `wi`): create, list, view, edit, close, comment, assign; run WIQL/saved queries; sprint/iteration views.
- **`pipeline`** (alias `run`): list, run/trigger, view, watch, logs, cancel, rerun.

### Cross-cutting
- **Auth:** `ado auth login|logout|status|token`, using Personal Access Tokens and Microsoft Entra ID (interactive/device-code). Credentials stored in the OS keychain.
- **Context detection:** infer organization, project and repository from the git remote (`dev.azure.com`, `ssh.dev.azure.com`, legacy `*.visualstudio.com`). Explicit `--org/--project/--repo` flags and `ADO_*` env vars override.
- **Scripting output:** `--json <fields>`, `--jq`, `--template`. TTY-aware formatting (tables for humans, plain/tab-separated when piped). `ado api <path>` for raw authenticated REST calls.
- **Aliases & extensions:** `ado alias set/list/delete`. `ado extension install/list/remove` for `ado-<name>` executables, matching gh's extension model.
- **Browse:** `--web` / `ado browse` opens the matching page in the browser.

## Scope & Targets
- **Phase 1:** Azure DevOps Services (cloud: `dev.azure.com`, `*.visualstudio.com`).
- **Later:** Azure DevOps Server (on-prem), via a configurable host.

## Distribution
- Open source.
- Single, dependency-free cross-platform binary (Windows, macOS, Linux; amd64/arm64).
- Shipped through GitHub Releases, Homebrew, winget and Scoop.

## Success Criteria
- A `gh` user can do the core PR loop (create, review, checkout, merge) without reading docs.
- Every list/view command supports `--json` and is safe to script against.
- Zero-config inside a cloned Azure DevOps repo once authenticated.
