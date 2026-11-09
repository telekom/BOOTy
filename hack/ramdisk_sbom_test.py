"""Tests for hack/ramdisk_sbom.py. Run: python3 -B hack/ramdisk_sbom_test.py"""

import gzip
import json
import pathlib
import stat
import sys
import tempfile
import textwrap
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import ramdisk_sbom  # noqa: E402

ELF = b"\x7fELF\x02\x01\x01"

# Stands in for syft: lists every file of the scanned tree, as syft does
# with file metadata enabled, and records the dpkg status it saw.
FAKE_SYFT = textwrap.dedent(
    """\
    import json, os, pathlib, sys
    args = sys.argv[1:]
    root = pathlib.Path(args[args.index("--base-path") + 1])
    outs = dict(a.split("=", 1) for a in args if "=" in a and a.split("=")[0].endswith("-json"))
    files = sorted("/" + p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file())
    seen = {f: (root / f[1:]).read_text() for f in files if f.startswith(("/var/lib/dpkg/", "/etc/os-release"))}
    pathlib.Path(outs["cyclonedx-json"]).write_text(json.dumps({"seen": seen, "components":
        [{"type": "file", "name": f} for f in files]}))
    pathlib.Path(outs["spdx-json"]).write_text(json.dumps({
        "packages": [{"SPDXID": "SPDXRef-root", "name": "root"}],
        "files": [{"SPDXID": "SPDXRef-File-%d" % i, "fileName": f} for i, f in enumerate(files)],
        "relationships": [{"spdxElementId": "SPDXRef-DOCUMENT", "relatedSpdxElement": "SPDXRef-root", "relationshipType": "DESCRIBES"}]
            + [{"spdxElementId": "SPDXRef-root", "relatedSpdxElement": "SPDXRef-File-%d" % i, "relationshipType": "CONTAINS"} for i, _ in enumerate(files)]}))
    """
)


def write(path, data=b""):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data if isinstance(data, bytes) else data.encode())


def newc(root):
    """A newc cpio archive of root, with a hard link pair and a device node."""
    out, ino = bytearray(), 0

    def entry(name, mode, data=b"", nlink=1, ino_=None):
        nonlocal ino
        ino += 1
        name = name.encode() + b"\0"
        fields = [ino_ or ino, mode, 0, 0, nlink, 0, len(data), 0, 0, 0, 0, len(name), 0]
        out.extend(b"070701" + b"".join(b"%08X" % f for f in fields) + name)
        out.extend(b"\0" * (-len(out) % 4) + data)
        out.extend(b"\0" * (-len(out) % 4))

    for p in sorted(root.rglob("*")):
        rel = p.relative_to(root).as_posix()
        if p.is_symlink():
            entry(rel, stat.S_IFLNK | 0o777, str(p.readlink()).encode())
        elif p.is_dir():
            entry(rel, stat.S_IFDIR | 0o755)
        else:
            entry(rel, stat.S_IFREG | 0o755, p.read_bytes())
    entry("dev/console", stat.S_IFCHR | 0o600)
    entry("bin/hard1", stat.S_IFREG | 0o755, b"", nlink=2, ino_=999)
    entry("bin/hard2", stat.S_IFREG | 0o755, ELF, nlink=2, ino_=999)
    entry("TRAILER!!!", 0)
    return bytes(out)


class RamdiskSBOMTest(unittest.TestCase):
    def setUp(self):
        tmp = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.tmp = tmp
        self.rootfs, self.inputs = tmp / "rootfs", tmp / "inputs"
        self.syft = tmp / "syft.py"
        self.syft.write_text("#!" + sys.executable + "\n" + FAKE_SYFT)
        self.syft.chmod(0o755)

        write(self.rootfs / "init", ELF + b"\xff Go buildinf:")
        write(self.rootfs / "bin/wipefs", ELF)
        write(self.rootfs / "bin/sfdisk", ELF)
        write(self.rootfs / "bin/ambiguous", ELF)
        write(self.rootfs / "bin/gpgv", ELF)
        write(self.rootfs / "etc/passwd", "root:x:0:0::/:/bin/sh\n")
        write(self.rootfs / "modules/xfs.ko", ELF)
        (self.rootfs / "bin/sh").symlink_to("busybox")

        tools = self.inputs / "dpkg/tools"
        write(
            tools / "status",
            "Package: util-linux\nStatus: install ok installed\nVersion: 2.41-1\nArchitecture: amd64\n"
            "Description: tools\n more text\n\n"
            "Package: fdisk\nStatus: install ok installed\nVersion: 2.41-1\n\n"
            "Package: passwd\nStatus: install ok installed\nVersion: 4.17-1\n\n"
            "Package: removed\nStatus: deinstall ok config-files\nVersion: 1\n\n"
            "Package: a\nStatus: install ok installed\nVersion: 1\n\n"
            "Package: apt\nStatus: install ok installed\nVersion: 3\n\n"
            "Package: b\nStatus: install ok installed\nVersion: 1\n",
        )
        write(tools / "info/util-linux:amd64.list", "/.\n/usr/sbin\n/usr/sbin/wipefs\n")
        write(tools / "info/fdisk.list", "/usr/sbin/sfdisk\n")
        write(tools / "info/passwd.list", "/usr/bin/passwd\n")
        write(tools / "info/a.list", "/usr/bin/ambiguous\n/usr/bin/gpgv\n")
        write(tools / "info/apt.list", "/usr/lib/apt/methods/gpgv\n/etc/network/if-up.d/wipefs\n")
        write(tools / "info/b.list", "/usr/sbin/ambiguous\n")
        write(
            self.inputs / "dpkg/kernel/status",
            "Package: linux-image-6.12-amd64\nVersion: 6.12.1-1\nStatus: install ok installed\n\n",
        )
        write(self.inputs / "dpkg/kernel/info/linux-image-6.12-amd64.list", "/usr/lib/modules/6.12/kernel/fs/xfs/xfs.ko.xz\n")
        write(self.inputs / "os-release", 'ID=debian\nVERSION_ID="13"\n')
        write(
            self.inputs / "source/util-linux.json",
            json.dumps({"name": "util-linux", "version": "abc", "purl": "pkg:github/util-linux/util-linux@abc", "files": ["bin/sfdisk"]}),
        )
        write(
            self.inputs / "source/lvm2.json",
            json.dumps({"name": "lvm2", "version": "2", "purl": "pkg:generic/lvm2@2", "files": ["sbin/lvm"]}),
        )

    def run_sbom(self):
        ramdisk = self.tmp / "test-amd64-initramfs.cpio.gz"
        with gzip.open(ramdisk, "wb") as f:
            f.write(newc(self.rootfs))
        ramdisk_sbom.main(["x", str(self.syft), str(self.inputs), str(ramdisk)])
        with open(self.tmp / "test-amd64-initramfs.cdx.json") as f:
            cdx = json.load(f)
        with open(self.tmp / "test-amd64-initramfs.spdx.json") as f:
            spdx = json.load(f)
        return cdx, spdx

    def test_attributes_elf_files_to_single_owning_package(self):
        packages, owners = ramdisk_sbom.read_dpkg(self.inputs)
        self.assertNotIn("removed", packages)
        owned, unowned = ramdisk_sbom.attribute(self.rootfs, packages, owners, {"bin/sfdisk"})
        self.assertEqual(
            dict(owned),
            {"util-linux": ["/bin/wipefs"], "linux-image-6.12-amd64": ["/modules/xfs.ko"], "a": ["/bin/gpgv"]},
        )
        # Two packages ship "ambiguous" in a bin directory; apt's gpgv is not
        # in one, and its /etc file does not count; passwd is not an ELF
        # file; init is Go.
        self.assertEqual(unowned, ["bin/ambiguous"])

    def test_writes_dpkg_database_that_syft_sees_and_drops_it_from_file_lists(self):
        cdx, spdx = self.run_sbom()

        seen = cdx["seen"]
        status = seen["/var/lib/dpkg/status"]
        self.assertIn("Package: util-linux\n", status)
        self.assertIn(" more text", status)
        self.assertIn("Package: linux-image-6.12-amd64\n", status)
        self.assertNotIn("Package: fdisk", status)
        self.assertEqual(seen["/var/lib/dpkg/info/util-linux.list"], "/bin/wipefs\n")
        self.assertEqual(seen["/etc/os-release"], 'ID=debian\nVERSION_ID="13"\n')

        files = [c["name"] for c in cdx["components"] if c["type"] == "file"]
        self.assertIn("/bin/wipefs", files)
        self.assertIn("/etc/passwd", files)
        # Both names of a hard link pair, no device nodes.
        self.assertIn("/bin/hard1", files)
        self.assertIn("/bin/hard2", files)
        self.assertNotIn("/dev/console", files)
        self.assertFalse([f for f in files if f.startswith("/var/lib/dpkg") or f == "/etc/os-release"])
        spdx_files = {f["SPDXID"]: f["fileName"] for f in spdx["files"]}
        self.assertNotIn("/etc/os-release", spdx_files.values())
        rel_ids = {r["relatedSpdxElement"] for r in spdx["relationships"]}
        self.assertFalse([i for i in rel_ids if i.startswith("SPDXRef-File-") and i not in spdx_files])

        # util-linux (sfdisk) is in the tree, lvm2 (sbin/lvm) is not.
        sources = [c for c in cdx["components"] if c["type"] == "application"]
        self.assertEqual([c["purl"] for c in sources], ["pkg:github/util-linux/util-linux@abc"])
        pkg = next(p for p in spdx["packages"] if p["name"] == "util-linux")
        sfdisk = next(i for i, f in spdx_files.items() if f == "/bin/sfdisk")
        self.assertIn(
            {"spdxElementId": pkg["SPDXID"], "relatedSpdxElement": sfdisk, "relationshipType": "CONTAINS"},
            spdx["relationships"],
        )

    def test_refuses_a_tree_with_a_package_database(self):
        write(self.rootfs / "etc/os-release", "ID=other\n")
        with self.assertRaises(SystemExit):
            self.run_sbom()


if __name__ == "__main__":
    unittest.main()
