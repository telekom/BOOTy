---
section: Behaviour changes in all modes
---

- A successful provision writes a provisioning report to
  `/etc/booty/provision.json` in the installed OS (new step
  `write-provision-report`, failure code `BOOTY-OS-016`) and sends it as a
  `provisioning report` log line to `LOG_URL`. The file is best effort: a
  failed write logs a warning. `PROVISION_REPORT_PATH` changes the path;
  `none` writes no file. See
  [docs/provisioning-report.md](../docs/provisioning-report.md).
