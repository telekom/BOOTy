---
section: Behaviour changes in all modes
---

- New `LOG_LEVEL` (`debug`, `info`, `warn`, `error`) and `LOG_FORMAT`
  (`text`, `json`) vars keys, also read from the kernel command line. The
  defaults `info` and `text` keep the current output. `LOG_LEVEL` applies to
  the console and to `LOG_URL`; an invalid vars value stops the run.
  `booty-esp-sync` gets `--log-level` and `--log-format` (env `LOG_LEVEL`,
  `LOG_FORMAT`) and now logs its fatal error as an `ERROR` line
  (`booty-esp-sync failed error=...`) instead of the bare error text.
  [docs/logging.md](../docs/logging.md) documents what each level logs.
