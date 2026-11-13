---
section: Behaviour changes in all modes
---

- New `LOG_LEVEL` (`debug`, `info`, `warn`/`warning`, `error`) and
  `LOG_FORMAT` (`text`, `json`) vars keys, also read from the kernel command
  line. The defaults `info` and `text` keep the current output. `LOG_LEVEL`
  applies to the console and to `LOG_URL`; an invalid value logs a warning
  and uses the default, it does not stop the run. The console log lines are
  now redacted (secret attribute keys, tokens, keys and credentials in the
  text) also without `LOG_URL`.
  `booty-esp-sync` gets `--log-level` and `--log-format` (env `LOG_LEVEL`,
  `LOG_FORMAT`; an invalid value falls back with a warning) and now logs its
  fatal error as an `ERROR` line (`booty-esp-sync failed error=...`) instead
  of the bare error text.
  [docs/logging.md](../docs/logging.md) documents what each level logs.
