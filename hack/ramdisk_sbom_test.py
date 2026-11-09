"""Tests for hack/ramdisk_sbom.py. Run: python3 -B hack/ramdisk_sbom_test.py"""

import gzip
import hashlib
import io
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
    import hashlib, json, os, pathlib, sys
    args = sys.argv[1:]
    root = pathlib.Path(args[args.index("--base-path") + 1])
    outs = dict(a.split("=", 1) for a in args if "=" in a and a.split("=")[0].endswith("-json"))
    files = sorted("/" + p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file())
    seen = {f: (root / f[1:]).read_text() for f in files if f.startswith(("/var/lib/dpkg/", "/etc/os-release"))}
    pathlib.Path(outs["cyclonedx-json"]).write_text(json.dumps({"seen": seen, "components":
        [{"type": "file", "name": f} for f in files]}))
    pathlib.Path(outs["spdx-json"]).write_text(json.dumps({
        "packages": [{"SPDXID": "SPDXRef-root", "name": "root"}],
        "files": [{"SPDXID": "SPDXRef-File-%d" % i, "fileName": f, "checksums": [
            {"algorithm": "SHA1", "checksumValue": hashlib.sha1((root / f[1:]).read_bytes()).hexdigest()}]} for i, f in enumerate(files)],
        "relationships": [{"spdxElementId": "SPDXRef-DOCUMENT", "relatedSpdxElement": "SPDXRef-root", "relationshipType": "DESCRIBES"}]
            + [{"spdxElementId": "SPDXRef-root", "relatedSpdxElement": "SPDXRef-File-%d" % i, "relationshipType": "CONTAINS"} for i, _ in enumerate(files)]}))
    """
)


def write(path, data=b""):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data if isinstance(data, bytes) else data.encode())


def entries(root):
    """(name, mode, data) for the files, directories and links of root."""
    for p in sorted(root.rglob("*")):
        rel = p.relative_to(root).as_posix()
        if p.is_symlink():
            yield rel, stat.S_IFLNK | 0o777, str(p.readlink()).encode()
        elif p.is_dir():
            yield rel, stat.S_IFDIR | 0o755, b""
        else:
            yield rel, stat.S_IFREG | (p.stat().st_mode & 0o777), p.read_bytes()


def newc(items):
    """A newc cpio archive of (name, mode, data[, nlink, ino]) items."""
    out = bytearray()
    for i, (name, mode, data, *link) in enumerate(list(items) + [("TRAILER!!!", 0, b"")]):
        nlink, ino = link or (1, i + 1)
        name = name.encode() + b"\0"
        fields = [ino, mode, 0, 0, nlink, 0, len(data), 0, 0, 0, 0, len(name), 0]
        out.extend(b"070701" + b"".join(b"%08X" % f for f in fields) + name)
        out.extend(b"\0" * (-len(out) % 4) + data)
        out.extend(b"\0" * (-len(out) % 4))
    return bytes(out)


def source(name, data):
    return json.dumps({"name": name, "version": "1", "purl": f"pkg:generic/{name}@1", "sha256": hashlib.sha256(data).hexdigest()})


SFDISK_BUILT = ELF + b"sfdisk built from source"
SFDISK_DEBIAN = ELF + b"sfdisk from Debian"
LIBFOO = ELF + b"libfoo"


class UnpackTest(unittest.TestCase):
    def unpack(self, items):
        dest = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        ramdisk_sbom.unpack(io.BytesIO(newc(items)), dest)
        return dest

    def test_hard_links_device_nodes_and_modes(self):
        reg = stat.S_IFREG | 0o755
        dest = self.unpack([
            (".", stat.S_IFDIR | 0o755, b""),
            ("./bin", stat.S_IFDIR | 0o755, b""),
            ("dev", stat.S_IFDIR | 0o755, b""),
            ("dev/console", stat.S_IFCHR | 0o600, b""),
            # newc: data with the last name; another archive: with the first.
            ("bin/a1", reg, b"", 2, 100), ("bin/a2", reg, b"A", 2, 100),
            ("bin/b1", reg, b"B", 2, 101), ("bin/b2", reg, b"", 2, 101),
            # Empty files linked together.
            ("bin/e1", stat.S_IFREG | 0o644, b"", 2, 102), ("bin/e2", stat.S_IFREG | 0o644, b"", 2, 102),
        ])
        for name, data in {"a1": b"A", "a2": b"A", "b1": b"B", "b2": b"B", "e1": b"", "e2": b""}.items():
            self.assertEqual((dest / "bin" / name).read_bytes(), data, name)
        self.assertEqual(stat.S_IMODE((dest / "bin/a1").stat().st_mode), 0o755)
        self.assertEqual(stat.S_IMODE((dest / "bin/e1").stat().st_mode), 0o644)
        self.assertFalse((dest / "dev/console").exists())

    def test_read_only_hard_links_and_unreadable_files(self):
        ro = stat.S_IFREG | 0o444
        dest = self.unpack([
            ("r1", ro, b"", 2, 100), ("r2", ro, b"R", 2, 100),
            ("x", stat.S_IFREG | 0o111, b"X"),
        ])
        self.assertEqual((dest / "r1").read_bytes(), b"R")
        self.assertEqual((dest / "x").read_bytes(), b"X")

    def test_refuses_paths_out_of_the_tree(self):
        outside = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        for items in (
            [("../x", stat.S_IFREG | 0o644, b"x")],
            [("/x", stat.S_IFREG | 0o644, b"x")],
            # Through a link that an earlier entry made.
            [("esc", stat.S_IFLNK | 0o777, str(outside).encode()), ("esc/pwned", stat.S_IFREG | 0o644, b"x")],
        ):
            with self.assertRaises(SystemExit):
                self.unpack(items)
        self.assertFalse((outside / "pwned").exists())

    def test_does_not_write_through_a_link(self):
        outside = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory())) / "target"
        with self.assertRaises(OSError):
            self.unpack([("f", stat.S_IFLNK | 0o777, str(outside).encode()), ("f", stat.S_IFREG | 0o644, b"x")])
        self.assertFalse(outside.exists())


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
        write(self.rootfs / "bin/sfdisk", SFDISK_BUILT)
        write(self.rootfs / "bin/ambiguous", ELF)
        write(self.rootfs / "bin/gpgv", ELF)
        write(self.rootfs / "lib/libfoo.so.1", LIBFOO)
        write(self.rootfs / "etc/passwd", "root:x:0:0::/:/bin/sh\n")
        write(self.rootfs / "modules/xfs.ko", ELF)
        (self.rootfs / "bin/sh").symlink_to("busybox")

        tools = self.inputs / "dpkg/tools"
        write(
            tools / "status",
            "Package: util-linux\nStatus: install ok installed\nVersion: 2.41-1\nArchitecture: amd64\n"
            "Description: tools\n more text\n\n"
            "Package: fdisk\nStatus: install ok installed\nVersion: 2.41-1\n\n"
            "Package: libfoo1\nStatus: install ok installed\nVersion: 1.0-1\n\n"
            "Package: passwd\nStatus: install ok installed\nVersion: 4.17-1\n\n"
            "Package: removed\nStatus: deinstall ok config-files\nVersion: 1\n\n"
            "Package: a\nStatus: install ok installed\nVersion: 1\n\n"
            "Package: apt\nStatus: install ok installed\nVersion: 3\n\n"
            "Package: b\nStatus: install ok installed\nVersion: 1\n",
        )
        write(tools / "info/util-linux:amd64.list", "/.\n/usr/sbin\n/usr/sbin/wipefs\n")
        write(tools / "info/fdisk.list", "/usr/sbin/sfdisk\n")
        write(tools / "info/fdisk.md5sums", hashlib.md5(SFDISK_DEBIAN).hexdigest() + "  usr/sbin/sfdisk\n")
        # Another name than in the ramdisk: only the content matches.
        write(tools / "info/libfoo1:amd64.md5sums", hashlib.md5(LIBFOO).hexdigest() + "  usr/lib/x86_64-linux-gnu/libfoo.so.1.0\n")
        write(tools / "info/passwd.list", "/usr/bin/passwd\n")
        write(tools / "info/a.list", "/usr/bin/ambiguous\n/usr/bin/gpgv\n")
        write(tools / "info/apt.list", "/usr/lib/apt/methods/gpgv\n/etc/network/if-up.d/wipefs\n")
        write(tools / "info/b.list", "/usr/sbin/ambiguous\n")
        # frr is first by name; the tools version of a package wins.
        write(
            self.inputs / "dpkg/frr/status",
            "Package: libfoo1\nStatus: install ok installed\nVersion: 0.9-1\n\n"
            "Package: frr\nStatus: install ok installed\nVersion: 10-1\n",
        )
        write(self.inputs / "dpkg/frr/frrouting-packages", "frr\nfrr-pythontools\n")
        write(
            self.inputs / "dpkg/kernel/status",
            "Package: linux-image-6.12-amd64\nVersion: 6.12.1-1\nStatus: install ok installed\n\n",
        )
        write(self.inputs / "dpkg/kernel/info/linux-image-6.12-amd64.list", "/usr/lib/modules/6.12/kernel/fs/xfs/xfs.ko.xz\n")
        write(self.inputs / "os-release", 'ID=debian\nVERSION_ID="13"\n')
        write(self.inputs / "source/util-linux.json", source("util-linux", SFDISK_BUILT))
        write(self.inputs / "source/lvm2.json", source("lvm2", ELF + b"lvm"))

    def run_sbom(self, extra=()):
        ramdisk = self.tmp / "test-amd64-initramfs.cpio.gz"
        with gzip.open(ramdisk, "wb") as f:
            f.write(newc(list(entries(self.rootfs)) + list(extra)))
        ramdisk_sbom.main(["x", str(self.syft), str(self.inputs), str(ramdisk)])
        with open(self.tmp / "test-amd64-initramfs.cdx.json") as f:
            cdx = json.load(f)
        with open(self.tmp / "test-amd64-initramfs.spdx.json") as f:
            spdx = json.load(f)
        return cdx, spdx

    def attribute(self):
        packages, digests, owners, _ = ramdisk_sbom.read_dpkg(self.inputs)
        return packages, ramdisk_sbom.attribute(self.rootfs, packages, digests, owners, ramdisk_sbom.read_sources(self.inputs))

    def test_attributes_files_to_packages_and_built_components(self):
        packages, (owned, built, unowned) = self.attribute()
        self.assertNotIn("removed", packages)
        self.assertIn("Version: 1.0-1", packages["libfoo1"])
        self.assertEqual(
            dict(owned),
            {
                "util-linux": ["/bin/wipefs"],
                "linux-image-6.12-amd64": ["/modules/xfs.ko"],
                "a": ["/bin/gpgv"],
                "libfoo1": ["/lib/libfoo.so.1"],
            },
        )
        self.assertEqual(dict(built), {"util-linux": ["/bin/sfdisk"]})
        # Two packages ship "ambiguous" in a bin directory; apt's gpgv is not
        # in one, and its /etc file does not count; passwd is not an ELF
        # file; init is Go.
        self.assertEqual(unowned, ["bin/ambiguous"])

    def test_debian_sfdisk_is_not_the_source_build(self):
        # slim ships Debian's sfdisk under the same name.
        write(self.rootfs / "bin/sfdisk", SFDISK_DEBIAN)
        _, (owned, built, _) = self.attribute()
        self.assertEqual(owned["fdisk"], ["/bin/sfdisk"])
        self.assertNotIn("util-linux", built)

    def test_writes_dpkg_database_that_syft_sees_and_drops_it_from_file_lists(self):
        cdx, spdx = self.run_sbom(
            [("bin/hard1", stat.S_IFREG | 0o755, b"", 2, 999), ("bin/hard2", stat.S_IFREG | 0o755, ELF, 2, 999),
             ("dev/console", stat.S_IFCHR | 0o600, b"")]
        )

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

        # util-linux (sfdisk) is in the tree, lvm2 is not.
        built = [c for c in cdx["components"] if c["type"] == "application"]
        self.assertEqual([c["purl"] for c in built], ["pkg:generic/util-linux@1"])
        self.assertEqual(built[0]["properties"], [{"name": "booty:ramdisk:file", "value": "/bin/sfdisk"}])
        pkg = next(p for p in spdx["packages"] if p["name"] == "util-linux")
        sfdisk = next(i for i, f in spdx_files.items() if f == "/bin/sfdisk")
        self.assertIn(
            {"spdxElementId": pkg["SPDXID"], "relatedSpdxElement": sfdisk, "relationshipType": "CONTAINS"},
            spdx["relationships"],
        )
        self.assertTrue(pkg["filesAnalyzed"])
        self.assertEqual(
            pkg["packageVerificationCode"]["packageVerificationCodeValue"],
            hashlib.sha1(hashlib.sha1(SFDISK_BUILT).hexdigest().encode()).hexdigest(),
        )

    def test_refuses_a_tree_with_os_release(self):
        (self.rootfs / "etc/os-release").symlink_to("/nonexistent")
        with self.assertRaises(SystemExit):
            self.run_sbom()

    def test_refuses_links_out_of_the_tree_on_the_added_paths(self):
        for link in ("var", "var/lib", "etc"):
            with self.subTest(link=link):
                outside = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
                path = self.rootfs / link
                (path / "passwd").unlink(missing_ok=True)
                if path.is_dir() and not path.is_symlink():
                    path.rmdir()
                path.parent.mkdir(parents=True, exist_ok=True)
                path.symlink_to(outside)
                with self.assertRaises(SystemExit):
                    self.run_sbom()
                self.assertEqual(list(outside.iterdir()), [])
                path.unlink()

    def test_frr_repository_packages_get_their_own_purl_namespace(self):
        frr = {"frr"}
        self.assertEqual(ramdisk_sbom.fix_purl("pkg:deb/debian/frr@10-1?distro=debian-13", frr), "pkg:deb/frrouting/frr@10-1?distro=debian-13")
        self.assertEqual(ramdisk_sbom.fix_purl("pkg:deb/debian/libc6@2.41", frr), "pkg:deb/debian/libc6@2.41")
        self.assertEqual(ramdisk_sbom.fix_purl("pkg:deb/debian/frr-other@1", frr), "pkg:deb/debian/frr-other@1")


if __name__ == "__main__":
    unittest.main()
