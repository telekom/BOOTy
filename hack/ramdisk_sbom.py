#!/usr/bin/env python3
"""Write CycloneDX and SPDX SBOMs of the content of BOOTy ramdisks.

usage: ramdisk_sbom.py <syft> <sbom-inputs> <ramdisk>...

  syft         path of the syft binary
  sbom-inputs  the output of `docker buildx build --target sbom-inputs`, or
               an empty directory for a ramdisk without Debian files (micro)
  ramdisk      a .cpio.zst or .cpio.gz newc archive; writes <name>.cdx.json
               and <name>.spdx.json next to it, <name> without .cpio.*

The ramdisk has no package database: the build copies single files out of
Debian stages and renames them (/usr/sbin/wipefs becomes /bin/wipefs). syft
alone therefore finds only the Go modules, busybox and the few Debian
packages that carry ELF package notes. This script unpacks the ramdisk into
a scratch directory, gives every ELF file the Debian package that ships a
file of the same name in one of the build stages (kernel modules match
their compressed .ko.xz), and writes those packages as a dpkg database with
the ramdisk paths, plus the os-release of the build stages. syft then
reports them like an installed Debian system, with the ramdisk files each
package owns. Files built from source (LVM2, sfdisk) are added as
components from sbom-inputs/source. Every file of the ramdisk is listed
with its SHA-256; the added database files and /etc/os-release are removed
from the file lists again.
"""

import gzip
import io
import json
import os
import pathlib
import re
import stat
import subprocess
import sys
import tempfile
from collections import defaultdict

DPKG = "var/lib/dpkg"
ADDED = ("/" + DPKG + "/", "/etc/os-release")
KO = re.compile(r"\.ko\.(xz|zst|gz)$")
GO_BUILDINFO = b"\xff Go buildinf:"


def read_exact(stream, n):
    data = stream.read(n)
    if len(data) != n:
        sys.exit("ramdisk-sbom: truncated cpio archive")
    return data


def unpack(stream, dest):
    """Unpack the directories, regular files and links of a newc cpio
    stream into dest. Device nodes carry no content and are skipped."""
    links = {}
    while True:
        hdr = read_exact(stream, 110)
        if hdr[:6] not in (b"070701", b"070702"):
            sys.exit("ramdisk-sbom: not a newc cpio archive")
        ino, mode, _, _, nlink, _, size = (int(hdr[6 + 8 * i : 14 + 8 * i], 16) for i in range(7))
        namesize = int(hdr[94:102], 16)
        name = read_exact(stream, namesize)[:-1].decode()
        read_exact(stream, -(110 + namesize) % 4)
        data = read_exact(stream, size)
        read_exact(stream, -size % 4)
        if name == "TRAILER!!!":
            return
        rel = pathlib.PurePosixPath(name)
        if rel.is_absolute() or ".." in rel.parts:
            sys.exit(f"ramdisk-sbom: unsafe cpio entry {name}")
        path = dest / rel
        if stat.S_ISDIR(mode):
            path.mkdir(parents=True, exist_ok=True)
        elif stat.S_ISLNK(mode):
            path.symlink_to(data.decode())
        elif stat.S_ISREG(mode):
            # newc stores the data of a hard link set with its last name.
            names = links.setdefault(ino, []) if nlink > 1 else []
            names.append(path)
            if size or nlink == 1:
                for p in names:
                    p.write_bytes(data)
                links.pop(ino, None)


def open_ramdisk(path):
    if path.endswith(".cpio.gz"):
        return gzip.open(path)
    if path.endswith(".cpio.zst"):
        return io.BytesIO(subprocess.run(["zstd", "-dcq", path], check=True, stdout=subprocess.PIPE).stdout)
    sys.exit(f"ramdisk-sbom: {path} is not a .cpio.zst or .cpio.gz file")


def stanzas(text):
    """Yield (fields, raw) for each stanza of a dpkg status file."""
    for raw in text.split("\n\n"):
        raw = raw.strip("\n")
        if not raw:
            continue
        fields = {}
        for line in raw.splitlines():
            if line and not line[0].isspace() and ":" in line:
                key, _, value = line.partition(":")
                fields[key] = value.strip()
        yield fields, raw


def read_dpkg(inputs):
    """Return {package: stanza} and {file name: {(package, in a bin dir)}}."""
    packages, owners = {}, defaultdict(set)
    for status in sorted(inputs.glob("dpkg/*/status")):
        for fields, raw in stanzas(status.read_text()):
            name = fields.get("Package")
            if not name or "installed" not in fields.get("Status", "").split():
                continue
            seen = packages.get(name)
            if seen and seen[0] != fields.get("Version"):
                print(f"ramdisk-sbom: {name}: versions {seen[0]} and {fields.get('Version')} in the stages, keeping {seen[0]}", file=sys.stderr)
            packages.setdefault(name, (fields.get("Version"), raw))
        for listing in sorted((status.parent / "info").glob("*.list")):
            pkg = listing.name[: -len(".list")].split(":")[0]
            for line in listing.read_text().splitlines():
                # Configuration and documentation are never ELF files.
                if line.startswith(("/etc/", "/usr/share/")):
                    continue
                folder, _, base = line.rpartition("/")
                if base:
                    owners[KO.sub(".ko", base)].add((pkg, folder.endswith(("/bin", "/sbin"))))
    return {k: v[1] for k, v in packages.items()}, owners


def read_sources(inputs):
    return [json.loads(p.read_text()) for p in sorted(inputs.glob("source/*.json"))]


def attribute(rootfs, packages, owners, skip):
    """Map ELF files of rootfs to packages: {package: [/path]}, [unowned]."""
    owned, unowned = defaultdict(list), []
    for dirpath, dirnames, filenames in os.walk(rootfs):
        dirnames.sort()
        for f in sorted(filenames):
            path = pathlib.Path(dirpath, f)
            rel = path.relative_to(rootfs).as_posix()
            if path.is_symlink() or not path.is_file() or rel in skip:
                continue
            data = path.read_bytes()
            # Go binaries: syft reads their build info.
            if not data.startswith(b"\x7fELF") or GO_BUILDINFO in data:
                continue
            found = {(p, in_bin) for p, in_bin in owners.get(f, ()) if p in packages}
            cands = {p for p, _ in found}
            # An executable in bin/ or sbin/: prefer the package that ships
            # it in a bin directory (gpgv, not apt's gpgv method).
            if len(cands) > 1 and rel.split("/")[0] in ("bin", "sbin"):
                cands = {p for p, in_bin in found if in_bin}
            if len(cands) == 1:
                owned[cands.pop()].append("/" + rel)
            else:
                unowned.append(rel)
    return owned, unowned


def write_dpkg(rootfs, packages, owned):
    info = rootfs / DPKG / "info"
    info.mkdir(parents=True)
    with open(rootfs / DPKG / "status", "w") as out:
        for pkg in sorted(owned):
            out.write(packages[pkg] + "\n\n")
            (info / f"{pkg}.list").write_text("\n".join(sorted(owned[pkg])) + "\n")


def drop_added_files_cdx(bom, rootfs):
    """Name files by their ramdisk path (syft writes the scan path into
    CycloneDX file names) and drop the files this script added."""
    keep = []
    for c in bom.get("components", []):
        if c.get("type") == "file":
            c["name"] = "/" + c["name"].removeprefix(str(rootfs)).lstrip("/")
            if c["name"].startswith(ADDED):
                continue
        keep.append(c)
    bom["components"] = keep


def drop_added_files_spdx(doc):
    gone = {f["SPDXID"] for f in doc.get("files", []) if ("/" + f.get("fileName", "").lstrip("/")).startswith(ADDED)}
    doc["files"] = [f for f in doc.get("files", []) if f["SPDXID"] not in gone]
    doc["relationships"] = [
        r for r in doc.get("relationships", []) if r["spdxElementId"] not in gone and r["relatedSpdxElement"] not in gone
    ]


def add_sources_cdx(bom, sources):
    for s in sources:
        bom.setdefault("components", []).append(
            {
                "bom-ref": s["purl"],
                "type": "application",
                "name": s["name"],
                "version": s["version"],
                "purl": s["purl"],
                "properties": [{"name": "booty:ramdisk:file", "value": "/" + f} for f in s["files"]],
            }
        )


def add_sources_spdx(doc, sources):
    root = next(r["relatedSpdxElement"] for r in doc["relationships"] if r["relationshipType"] == "DESCRIBES")
    files = {"/" + f.get("fileName", "").lstrip("/"): f["SPDXID"] for f in doc.get("files", [])}
    for s in sources:
        spdxid = "SPDXRef-Package-source-" + re.sub(r"[^A-Za-z0-9.-]", "-", s["name"])
        doc["packages"].append(
            {
                "name": s["name"],
                "SPDXID": spdxid,
                "versionInfo": s["version"],
                "downloadLocation": "NOASSERTION",
                "filesAnalyzed": False,
                "licenseConcluded": "NOASSERTION",
                "licenseDeclared": "NOASSERTION",
                "copyrightText": "NOASSERTION",
                "externalRefs": [
                    {"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": s["purl"]}
                ],
            }
        )
        doc["relationships"].append({"spdxElementId": root, "relatedSpdxElement": spdxid, "relationshipType": "CONTAINS"})
        for f in s["files"]:
            if "/" + f in files:
                doc["relationships"].append(
                    {"spdxElementId": spdxid, "relatedSpdxElement": files["/" + f], "relationshipType": "CONTAINS"}
                )


def sbom(syft, inputs, ramdisk):
    prefix = re.sub(r"\.cpio\.(zst|gz)$", "", ramdisk)
    with tempfile.TemporaryDirectory() as tmp:
        rootfs = pathlib.Path(tmp).resolve()
        with open_ramdisk(ramdisk) as stream:
            unpack(stream, rootfs)
        if (rootfs / DPKG).exists() or (rootfs / "etc/os-release").exists():
            sys.exit(f"ramdisk-sbom: {ramdisk} already has a package database or /etc/os-release")

        packages, owners = read_dpkg(inputs)
        sources = [s for s in read_sources(inputs) if all((rootfs / f).is_file() for f in s["files"])]
        owned, unowned = attribute(rootfs, packages, owners, {f for s in sources for f in s["files"]})
        if owned:
            write_dpkg(rootfs, packages, owned)
            (rootfs / "etc").mkdir(exist_ok=True)
            (rootfs / "etc/os-release").write_bytes((inputs / "os-release").read_bytes())
        print(f"ramdisk-sbom: {ramdisk}: {sum(map(len, owned.values()))} ELF files in {len(owned)} Debian packages, {len(sources)} source components")
        for rel in unowned:
            print(f"ramdisk-sbom: {ramdisk}: no single Debian package ships {rel}")

        env = dict(os.environ, SYFT_CHECK_FOR_APP_UPDATE="false", SYFT_FILE_METADATA_SELECTION="all", SYFT_FILE_METADATA_DIGESTS="sha256")
        cdx, spdx = prefix + ".cdx.json", prefix + ".spdx.json"
        subprocess.run(
            # The dpkg database covers the Debian files better than their
            # ELF package notes, which would list some packages twice.
            [syft, "scan", f"dir:{rootfs}", "--base-path", str(rootfs), "--source-name", os.path.basename(ramdisk), "-q",
             "--select-catalogers", "-elf-binary-package-cataloger",
             "-o", f"cyclonedx-json={cdx}", "-o", f"spdx-json={spdx}"],
            check=True,
            env=env,
        )

        with open(cdx) as f:
            bom = json.load(f)
        drop_added_files_cdx(bom, rootfs)
    add_sources_cdx(bom, sources)
    with open(cdx, "w") as f:
        json.dump(bom, f, indent=2)
    with open(spdx) as f:
        doc = json.load(f)
    drop_added_files_spdx(doc)
    add_sources_spdx(doc, sources)
    with open(spdx, "w") as f:
        json.dump(doc, f, indent=2)


def main(argv):
    if len(argv) < 4:
        sys.exit(__doc__)
    for ramdisk in argv[3:]:
        sbom(argv[1], pathlib.Path(argv[2]), ramdisk)


if __name__ == "__main__":
    main(sys.argv)
