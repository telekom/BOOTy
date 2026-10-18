"""Tests for hack/changelog.py. Run: python3 -B hack/changelog_test.py"""

import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import changelog  # noqa: E402

FROZEN = """Intro text.

### Behaviour behind `X`

- legacy (#5)

### Fixes

- hand-written fix (#1)
"""


class RenderTest(unittest.TestCase):
    def test_merge(self):
        entries = [
            ("Fixes", changelog.entry_line("gobgp", "already listed (#1)")),
            ("Fixes", changelog.entry_line("gobgp", "new fix (#2)")),
            ("Features", changelog.entry_line(None, "new feature (#3)")),
            ("Dependencies", changelog.entry_line("deps", "bump x from 1 to 2 (#4)")),
        ]
        frags = [("Upgrade notes for CAPRF", "- check frr.conf"), ("Breaking changes", "- removed Y")]
        self.assertEqual(
            changelog.render(FROZEN, entries, frags),
            """Intro text.

### Behaviour behind `X`

- legacy (#5)

### Breaking changes

- removed Y

### Features

- new feature ([#3](https://github.com/telekom/BOOTy/pull/3))

### Fixes

- hand-written fix (#1)
- gobgp: new fix ([#2](https://github.com/telekom/BOOTy/pull/2))

### Dependencies

- bump x from 1 to 2 ([#4](https://github.com/telekom/BOOTy/pull/4))

### Upgrade notes for CAPRF

- check frr.conf""",
        )

    def test_empty(self):
        self.assertEqual(changelog.render("", [], []), "No changes.")


class ReleaseTest(unittest.TestCase):
    def test_release(self):
        text = """# Changelog

## [Unreleased]

old text

## [0.1.0] - 2026-01-01

- a

[Unreleased]: https://github.com/telekom/BOOTy/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/telekom/BOOTy/releases/tag/v0.1.0
"""
        out = changelog.release("0.2.0", "- b", text, "2026-02-02")
        self.assertEqual(
            out,
            """# Changelog

## [Unreleased]

## [0.2.0] - 2026-02-02

- b

## [0.1.0] - 2026-01-01

- a

[Unreleased]: https://github.com/telekom/BOOTy/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/telekom/BOOTy/releases/tag/v0.2.0
[0.1.0]: https://github.com/telekom/BOOTy/releases/tag/v0.1.0
""",
        )
        start, end = changelog.find_section(out, "0.2.0")
        self.assertEqual(out[start:end].strip(), "- b")


class FragmentTest(unittest.TestCase):
    def test_fragments(self):
        with tempfile.TemporaryDirectory() as tmp:
            d = pathlib.Path(tmp)
            (d / "README.md").write_text("ignored")
            (d / "123.md").write_text("---\nsection: Upgrade notes for CAPRF\n---\n\n- note\n")
            self.assertEqual(changelog.read_fragments(d), [("Upgrade notes for CAPRF", "- note")])
            (d / "bad.md").write_text("- no front matter\n")
            with self.assertRaises(SystemExit):
                changelog.read_fragments(d)


if __name__ == "__main__":
    unittest.main()
