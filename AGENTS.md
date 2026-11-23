# Agent instructions

This repository uses spec for spec-driven development.

- Specs live in specs/ (project/, features/, adr/, imported/); untyped
  ideas live in specs/inbox/ until promoted (spec promote).
- Each spec is markdown with YAML frontmatter (type, status, depends_on).
- Valid statuses are configured in .spec/config.yml (statuses:).
- .spec/facts.yml holds definite project facts, referenced as {fact:key}.
- Run spec check to validate specs and spec verify to run configured checks.
- When implementing a spec, update its frontmatter status to implemented.
- Implemented specs are living documentation: if a change alters behavior
  another spec describes, update that spec in the same change.
- Run spec drift to find specs whose covered code changed since they were
  last updated; fix them, or spec verified <id> if still accurate.
- A development role may be defined in .spec/role.md or selected via
  persona: in .spec/config.yml (shared library ~/.config/spec/personas/) —
  when set, adopt that persona and its conventions for all work here.
