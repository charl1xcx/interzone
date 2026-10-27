CI runs on GitHub (`.github/workflows/ci.yml`). This folder holds no
workflows on purpose: Forgejo reads `.forgejo/workflows` before
`.github/workflows`, so it no longer parses GitHub's file and warns about its
`permissions` fields on every commit.
