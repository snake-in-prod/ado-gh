# Project Workflow

## Guiding Principles

1. **The Plan is the Source of Truth:** All work must be tracked in `plan.md`
2. **The Tech Stack is Deliberate:** Changes to the tech stack must be documented in `tech-stack.md` *before* implementation
3. **Test-Driven Development:** Write unit tests before implementing functionality
4. **High Code Coverage:** Aim for >90% code coverage for all packages
5. **User Experience First:** Every decision should prioritize user experience (see `product-guidelines.md`)
6. **Non-Interactive & CI-Aware:** Prefer non-interactive commands. Use `CI=true` for watch-mode tools (tests, linters) to ensure single execution.
7. **Multi-Repo Projects:** If `conductor/repos.md` exists, the project is one system spread across several git repositories. Every git command in this workflow (commit, diff, log) runs in the repository that owns the files, as its Multi-Repo Protocol describes, and plan updates are committed in `conductor/`.

## Commit Policy

- **Commit once per phase**, not per task. Task work accumulates in the working tree until the phase checkpoint.
- **Task summaries live in commit messages**, not git notes. Never use `git notes`.
- **Commit messages are concise, one line**, Conventional Commits format: `<type>(<scope>): <description>`. No body unless strictly necessary.
- **No `Co-Authored-By` or other attribution trailers** in commit messages.

## Task Workflow

All tasks follow a strict lifecycle:

### Standard Task Workflow

1. **Select Task:** Choose the next available task from `plan.md` in sequential order

2. **Mark In Progress:** Before beginning work, edit `plan.md` and change the task from `[ ]` to `[~]`

3. **Write Failing Tests (Red Phase):**
   - Create a new `_test.go` file (or extend the existing one) for the feature or bug fix.
   - Write one or more unit tests that clearly define the expected behavior and acceptance criteria for the task. Prefer table-driven tests.
   - **CRITICAL:** Run the tests and confirm that they fail as expected. This is the "Red" phase of TDD. Do not proceed until you have failing tests.

4. **Implement to Pass Tests (Green Phase):**
   - Write the minimum amount of application code necessary to make the failing tests pass.
   - Run the test suite again and confirm that all tests now pass. This is the "Green" phase.

5. **Refactor (Optional but Recommended):**
   - With the safety of passing tests, refactor the implementation and test code to improve clarity and remove duplication without changing external behavior.
   - Rerun tests to ensure they still pass after refactoring.

6. **Verify Coverage:**
   ```bash
   go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out
   ```
   Target: >90% coverage for new code.

7. **Document Deviations:** If implementation differs from tech stack:
   - **STOP** implementation
   - Update `tech-stack.md` with new design
   - Add dated note explaining the change
   - Resume implementation

8. **Mark Task Complete:** Edit `plan.md` and change the task from `[~]` to `[x]`. Do not commit yet; the commit happens at the phase checkpoint.

### Phase Completion Verification and Checkpointing Protocol

**Trigger:** This protocol is executed immediately after a task is completed that also concludes a phase in `plan.md`.

1.  **Announce Protocol Start:** Inform the user that the phase is complete and the verification and checkpointing protocol has begun.

2.  **Ensure Test Coverage for Phase Changes:**
    -   **Step 2.1: Determine Phase Scope:** Read `plan.md` to find the Git commit SHA of the *previous* phase's checkpoint. If no previous checkpoint exists, the scope is all changes since the first commit (or all files, if there are no commits yet).
    -   **Step 2.2: List Changed Files:** Execute `git status --porcelain` and `git diff --name-only <previous_checkpoint_sha>` to list all files modified during this phase (including untracked files).
    -   **Step 2.3: Verify and Create Tests:** For each file in the list:
        -   **CRITICAL:** Exclude non-code files (e.g., `.json`, `.md`, `.yaml`).
        -   For each remaining `.go` file, verify a corresponding `_test.go` file exists.
        -   If a test file is missing, you **must** create one, following the naming convention and testing style of existing tests. The new tests **must** validate the functionality described in this phase's tasks (`plan.md`).

3.  **Execute Automated Tests with Proactive Debugging:**
    -   Before execution, you **must** announce the exact shell command you will use to run the tests.
    -   **Example Announcement:** "I will now run the automated test suite to verify the phase. **Command:** `go test ./... -race -cover`"
    -   Execute the announced command.
    -   If tests fail, you **must** inform the user and begin debugging. You may attempt to propose a fix a **maximum of two times**. If the tests still fail after your second proposed fix, you **must stop**, report the persistent failure, and ask the user for guidance.

4.  **Propose a Detailed, Actionable Manual Verification Plan:**
    -   **CRITICAL:** To generate the plan, first analyze `product.md`, `product-guidelines.md`, and `plan.md` to determine the user-facing goals of the completed phase.
    -   You **must** generate a step-by-step plan that walks the user through the verification process, including any necessary commands and specific, expected outcomes.
    -   The plan you present to the user **must** follow this format:

        ```
        The automated tests have passed. For manual verification, please follow these steps:

        **Manual Verification Steps:**
        1.  **Build the binary:** `go build -o bin/ado ./cmd/ado`
        2.  **Execute the following command in your terminal:** `./bin/ado pr list --json id,title`
        3.  **Confirm that you receive:** A JSON array of pull requests for the current repository.
        ```

5.  **Await Explicit User Feedback:**
    -   After presenting the detailed plan, ask the user for confirmation: "**Does this meet your expectations? Please confirm with yes or provide feedback on what needs to be changed.**"
    -   **PAUSE** and await the user's response. Do not proceed without an explicit yes or confirmation.

6.  **Create Phase Commit:**
    -   Stage all code and `plan.md` changes for the phase.
    -   Commit with a concise, one-line Conventional Commits message summarizing the phase (e.g., `feat(auth): add PAT login and keychain storage`). No body, no attribution trailers.

7.  **Record Phase Checkpoint SHA:**
    -   **Step 7.1: Get Commit Hash:** `git log -1 --format="%h"`.
    -   **Step 7.2: Update Plan:** In `plan.md`, append `[checkpoint: <sha>]` to the heading of the completed phase.
    -   **Step 7.3: Amend:** Stage `plan.md` and `git commit --amend --no-edit` so the checkpoint marker lands in the same commit. (The recorded SHA refers to the pre-amend commit; it still works as a diff base because only `plan.md` changed.)

8.  **Announce Completion:** Inform the user that the phase is complete and the checkpoint commit has been created.

### Quality Gates

Before marking any phase complete, verify:

- [ ] All tests pass (`go test ./... -race`)
- [ ] Code coverage meets requirements (>90%)
- [ ] Code follows project's code style guidelines (as defined in `code_styleguides/`)
- [ ] All exported identifiers have GoDoc comments
- [ ] No linting or static analysis errors (`golangci-lint run`, `go vet ./...`)
- [ ] `gofmt -l .` reports nothing
- [ ] Help text and docs updated if command surface changed
- [ ] No security vulnerabilities introduced (no tokens logged, `Authorization` redacted)

## Development Commands

### Setup
```bash
go mod tidy
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

### Daily Development
```bash
go build -o bin/ado ./cmd/ado   # build
go run ./cmd/ado <args>         # run
go test ./...                   # test
gofmt -w .                      # format
```

### Before Committing
```bash
gofmt -l . && go vet ./... && golangci-lint run && go test ./... -race -cover
```

## Testing Requirements

### Unit Testing
- Every package must have corresponding `_test.go` tests.
- Prefer table-driven tests; use `t.Cleanup` / `t.TempDir` / `t.Setenv` for setup and teardown.
- Stub all HTTP via the `httpmock` registry / `httptest`. No live network in unit tests.
- Use the test IOStreams to cover both TTY and non-TTY output.
- Test both success and failure cases (including exit codes and error messages).

### Command Testing
- Test each command end to end through its `RunE` with a fake Factory.
- Verify `--json` output fields, prompts (TTY) and fail-fast behavior (non-TTY).
- Verify auth-required and not-found error paths.

## Code Review Process

### Self-Review Checklist
Before requesting review:

1. **Functionality**
   - Feature works as specified
   - Edge cases handled
   - Error messages are actionable (what failed + next command)

2. **Code Quality**
   - Follows style guide
   - Clear names; no needless abstractions
   - Appropriate comments

3. **Testing**
   - Unit tests comprehensive
   - Coverage adequate (>90%)

4. **Security**
   - No hardcoded secrets
   - Tokens never printed, logged or placed in URLs
   - Input validation at trust boundaries (flags, remotes, API responses)

5. **gh Parity**
   - Command/flag names match `gh` where an equivalent exists
   - Divergences documented

## Commit Guidelines

### Message Format
```
<type>(<scope>): <description>
```
One line, concise. No body unless strictly necessary. No attribution trailers (`Co-Authored-By`, etc.).

### Types
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation only
- `style`: Formatting
- `refactor`: Code change that neither fixes a bug nor adds a feature
- `test`: Adding missing tests
- `chore`: Maintenance tasks
- `conductor`: Conductor plan/track files

### Examples
```bash
git commit -m "feat(auth): add PAT login and keychain storage"
git commit -m "fix(context): parse legacy visualstudio.com remotes"
git commit -m "test(pr): cover non-TTY create without title"
```

## Definition of Done

A phase is complete when:

1. All code implemented to specification
2. Unit tests written and passing
3. Code coverage meets project requirements (>90%)
4. Documentation complete (if applicable)
5. Code passes all configured linting and static analysis checks
6. All tasks marked `[x]` in `plan.md`
7. Changes committed with a concise one-line message, checkpoint SHA recorded

## Release Workflow

The full procedure is in [`docs/releasing.md`](../docs/releasing.md) (one-time setup, versioning, notes, monitoring, winget, prerelease, hotfix and rollback). Short checklist:

- [ ] CI green on `main` (linux/macos/windows tests, lint, snapshot, reproducibility, docs, install scripts)
- [ ] Release-prep PR `chore(release): prepare vX.Y.Z` merged: `CHANGELOG.md` regenerated with git-cliff, breaking changes called out
- [ ] Commit subjects since the last tag read as user-facing notes (no "address … review findings")
- [ ] Annotated tag `vX.Y.Z` (or `vX.Y.Z-rc.N`) on a commit of `main`, pushed one tag at a time; tags are never moved or deleted
- [ ] Release workflow green: signed checksums (cosign + GPG), SBOMs, attestations, notes; docs deployed (stable only)
- [ ] Smoke workflow green on all 6 platforms (and the channel jobs for stable releases)
- [ ] winget pull request followed until merged

## Emergency Procedures

### Critical Bug in a Release
1. Write a failing test for the bug on a branch from `main`
2. Implement the minimal fix and merge it to `main` (the release guard only accepts tags on `main`)
3. Release a patch version tagged from `main` (see the hotfix flow in `docs/releasing.md`)
4. Document in plan.md

### Leaked Credential
1. Revoke the token/PAT immediately
2. Patch the leak path (logging, debug output, URL)
3. Add a regression test asserting redaction
4. Release a patch version and document in the changelog
