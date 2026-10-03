# Issue tracker: GitHub

Issues, specs and tickets for this repo are GitHub issues on `luizfreitas27/ajai`. Use the `gh` CLI for all operations; it infers the repo from `git remote -v`.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."` (heredoc for multi-line bodies).
- **Read an issue**: `gh issue view <number> --comments`.
- **List issues**: `gh issue list --state open --json number,title,body,labels --jq '...'` with `--label` filters.
- **Comment**: `gh issue comment <number> --body "..."`.
- **Labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`.
- **Close**: `gh issue close <number> --comment "..."`.
- **Blocking edges between tickets**: GitHub native issue dependencies,
  `gh api --method POST repos/luizfreitas27/ajai/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`
  where the db id comes from `gh api repos/luizfreitas27/ajai/issues/<n> --jq .id`.

## Delivery rule

One ticket = one branch = one pull request into `main`, reviewed with `/code-review`. The user merges each PR before the next ticket's branch is cut; never stack branches.

## Pull requests as a triage surface

No.

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.
