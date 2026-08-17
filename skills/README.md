# Nous Agent Skills

This directory is the repository-owned catalog for Skill capabilities.

- `public/` contains versioned Skills distributed with Nous Agent.
- `custom/` is reserved for user-managed Skills and is ignored by Git except
  for its placeholder file.

The catalog is shared by the Go Harness and Go Gateway. It is intentionally
independent from any Agent implementation directory. Runtime deployments mount
it at `/opt/nous/skills` and may override the location with
`NOUS_SKILLS_PATH` or `NOUS_SKILLS_ROOT`.
