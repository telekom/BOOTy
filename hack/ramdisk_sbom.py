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
a scratch directory and gives each file a package:

- a file whose content a package of the build stages ships (dpkg md5sums);
- an ELF file that the build changed (stripped tools, decompressed kernel
  modules): the one package that ships a file of its name;
- a file built in the build (LVM2, sfdisk, the GRUB EFI loader): the
  component in sbom-inputs/source with its SHA-256.

It writes the Debian packages as a dpkg database with the ramdisk paths,
plus the os-release of the build stages, and runs syft, which reports them
like an installed Debian system with the files each package owns. Packages
of the FRR repository get the purl namespace frrouting. Every file of the
ramdisk is listed with its SHA-256; the added database files and
/etc/os-release are removed from the output again.
"""

import gzip
import hashlib
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
STAGES = ("tools", "kernel", "frr")
GO_BUILDINFO = b"\xff Go buildinf:"


def read_exact(stream, n):
    data = stream.read(n)
    if len(data) != n:
        sys.exit("ramdisk-sbom: truncated cpio archive")
    return data


def write_file(path, data, mode):
    # O_NOFOLLOW: never write through a link that an earlier entry made.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as f:
        f.write(data)
        # Owner read/write on top: a hard link set may be written again,
        # and attribute() reads every file. Nothing reads the mode back.
        os.fchmod(f.fileno(), stat.S_IMODE(mode) | 0o600)


def unpack(stream, dest):
    """Unpack the directories, regular files and links of a newc cpio
    stream into dest. Device nodes carry no content and are skipped."""
    dest = dest.resolve()
    links = {}
    while True:
        hdr = read_exact(stream, 110)
        if hdr[:6] not in (b"070701", b"070702"):
            sys.exit("ramdisk-sbom: not a newc cpio archive")
        f = [int(hdr[6 + 8 * i : 14 + 8 * i], 16) for i in range(13)]
        ino, mode, nlink, size, devmajor, devminor, namesize = f[0], f[1], f[4], f[6], f[7], f[8], f[11]
        name = read_exact(stream, namesize)[:-1].decode()
        read_exact(stream, -(110 + namesize) % 4)
        data = read_exact(stream, size)
        read_exact(stream, -size % 4)
        if name == "TRAILER!!!":
            return
        rel = pathlib.PurePosixPath(name)
        if not rel.parts:  # the root, "."
            continue
        path = dest / rel
        if rel.is_absolute() or ".." in rel.parts or not path.parent.resolve().is_relative_to(dest):
            sys.exit(f"ramdisk-sbom: unsafe cpio entry {name}")
        if stat.S_ISDIR(mode):
            path.mkdir(parents=True, exist_ok=True)
        elif stat.S_ISLNK(mode):
            path.symlink_to(data.decode())
        elif stat.S_ISREG(mode):
            write_file(path, data, mode)
            # A hard link set carries its data with one of its names
            # (newc: the last); give it to the other names.
            if nlink > 1:
                link = links.setdefault((devmajor, devminor, ino), {"names": [], "data": None})
                if size:
                    link["data"] = data
                    for p in link["names"]:
                        write_file(p, data, mode)
                elif link["data"] is not None:
                    write_file(path, link["data"], mode)
                link["names"].append(path)


def open_ramdisk(path):
    if path.endswith(".cpio.gz"):
        return gzip.open(path)
    if path.endswith(".cpio.zst"):
        return io.BytesIO(subprocess.run(["zstd", "-dcq", path], check=True, stdout=subprocess.PIPE).stdout)
    raise SystemExit(f"ramdisk-sbom: {path} is not a .cpio.zst or .cpio.gz file")


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


def stage_order(stage):
    # Where a package is in several stages, take the version of the stage
    # that most ramdisk files come from.
    return (STAGES.index(stage.name) if stage.name in STAGES else len(STAGES), stage.name)


def read_dpkg(inputs):
    """Return {package: stanza}, {md5: {package}}, {file name: {(package,
    in a bin dir)}} and the packages of the FRR repository."""
    packages, digests, owners, frrouting = {}, defaultdict(set), defaultdict(set), set()
    for stage in sorted(inputs.glob("dpkg/*"), key=stage_order):
        for fields, raw in stanzas((stage / "status").read_text()):
            name = fields.get("Package")
            if not name or "installed" not in fields.get("Status", "").split():
                continue
            if name not in packages:
                packages[name] = raw
            elif fields.get("Version") not in packages[name]:
                print(f"ramdisk-sbom: {name}: another version {fields.get('Version')} in {stage.name}, ignored", file=sys.stderr)
        for md5sums in sorted((stage / "info").glob("*.md5sums")):
            pkg = md5sums.name[: -len(".md5sums")].split(":")[0]
            for line in md5sums.read_text().splitlines():
                digests[line.split(" ", 1)[0]].add(pkg)
        for listing in sorted((stage / "info").glob("*.list")):
            pkg = listing.name[: -len(".list")].split(":")[0]
            for line in listing.read_text().splitlines():
                # Configuration and documentation are never ELF files.
                if line.startswith(("/etc/", "/usr/share/")):
                    continue
                folder, _, base = line.rpartition("/")
                if base:
                    owners[KO.sub(".ko", base)].add((pkg, folder.endswith(("/bin", "/sbin"))))
        if (stage / "frrouting-packages").exists():
            frrouting |= set((stage / "frrouting-packages").read_text().split())
    return packages, digests, owners, frrouting


def read_sources(inputs):
    return [json.loads(p.read_text()) for p in sorted(inputs.glob("source/*.json"))]


def attribute(rootfs, packages, digests, owners, sources):
    """Map the files of rootfs to Debian packages and source components.

    A file whose content a package ships (dpkg md5sums) belongs to it. An
    ELF file that the build changed (stripped binaries, decompressed kernel
    modules) belongs to the one package that ships a file of its name. A
    source component owns the files with its SHA-256. Returns {package:
    [/path]}, {source name: [/path]} and the unowned ELF files."""
    by_sha = {s["sha256"]: s["name"] for s in sources}
    owned, built, unowned = defaultdict(list), defaultdict(list), []
    for dirpath, dirnames, filenames in os.walk(rootfs):
        dirnames.sort()
        for f in sorted(filenames):
            path = pathlib.Path(dirpath, f)
            rel = path.relative_to(rootfs).as_posix()
            if path.is_symlink() or not path.is_file():
                continue
            data = path.read_bytes()
            sha = hashlib.sha256(data).hexdigest()
            if sha in by_sha:
                built[by_sha[sha]].append("/" + rel)
                continue
            # Empty files say nothing about their package.
            cands = {p for p in digests.get(hashlib.md5(data).hexdigest(), ()) if p in packages} if data else set()
            if len(cands) != 1:
                # Go binaries: syft reads their build info.
                if not data.startswith(b"\x7fELF") or GO_BUILDINFO in data:
                    continue
                found = {(p, in_bin) for p, in_bin in owners.get(f, ()) if p in packages}
                cands = {p for p, _ in found}
                # An executable in bin/ or sbin/: prefer the package that
                # ships it in a bin directory (gpgv, not apt's gpgv method).
                if len(cands) > 1 and rel.split("/")[0] in ("bin", "sbin"):
                    cands = {p for p, in_bin in found if in_bin}
            if len(cands) == 1:
                owned[cands.pop()].append("/" + rel)
            else:
                unowned.append(rel)
    return owned, built, unowned


def write_dpkg(rootfs, packages, owned):
    info = rootfs / DPKG / "info"
    info.mkdir(parents=True)
    with open(rootfs / DPKG / "status", "w") as out:
        for pkg in sorted(owned):
            out.write(packages[pkg] + "\n\n")
            (info / f"{pkg}.list").write_text("\n".join(sorted(owned[pkg])) + "\n")
            # syft takes the files of a package from its md5sums, not .list.
            (info / f"{pkg}.md5sums").write_text(
                "".join(f"{hashlib.md5((rootfs / f[1:]).read_bytes()).hexdigest()}  {f[1:]}\n" for f in sorted(owned[pkg]))
            )


def fix_purl(purl, frrouting):
    # The FRR packages come from the FRR repository, not from Debian: name
    # them so that a scanner does not match them to Debian advisories.
    m = re.match(r"pkg:deb/debian/([^@]+)@", purl or "")
    if m and m.group(1) in frrouting:
        return "pkg:deb/frrouting/" + purl[len("pkg:deb/debian/") :]
    return purl


def fix_cdx(bom, rootfs, frrouting):
    """Name files by their ramdisk path (syft writes the scan path into
    CycloneDX file names), drop the files this script added and the
    locations that point to them, and fix the FRR purls."""
    keep = []
    for c in bom.get("components", []):
        if c.get("type") == "file":
            c["name"] = "/" + c["name"].removeprefix(str(rootfs)).lstrip("/")
            if c["name"].startswith(ADDED):
                continue
        if "purl" in c:
            c["purl"] = fix_purl(c["purl"], frrouting)
        if "properties" in c:
            c["properties"] = [p for p in c["properties"] if not str(p.get("value", "")).startswith(ADDED)]
        keep.append(c)
    bom["components"] = keep


def fix_spdx(doc, frrouting):
    gone = {f["SPDXID"] for f in doc.get("files", []) if ("/" + f.get("fileName", "").lstrip("/")).startswith(ADDED)}
    doc["files"] = [f for f in doc.get("files", []) if f["SPDXID"] not in gone]
    doc["relationships"] = [
        r for r in doc.get("relationships", []) if r["spdxElementId"] not in gone and r["relatedSpdxElement"] not in gone
    ]
    for p in doc.get("packages", []):
        for ref in p.get("externalRefs", []):
            if ref.get("referenceType") == "purl":
                ref["referenceLocator"] = fix_purl(ref["referenceLocator"], frrouting)


def add_sources_cdx(bom, sources, built):
    for s in sources:
        bom.setdefault("components", []).append(
            {
                "bom-ref": s["purl"],
                "type": "application",
                "name": s["name"],
                "version": s["version"],
                "purl": s["purl"],
                "hashes": [{"alg": "SHA-256", "content": s["sha256"]}],
                "properties": [{"name": "booty:ramdisk:file", "value": f} for f in built[s["name"]]],
            }
        )


def verification_code(files):
    """The SPDX 2.x package verification code of files: the SHA-1 of their
    sorted SHA-1s."""
    sha1s = sorted(c["checksumValue"] for f in files for c in f.get("checksums", []) if c.get("algorithm") == "SHA1")
    if len(sha1s) != len(files):
        sys.exit("ramdisk-sbom: syft wrote a file without a SHA-1")
    return {"packageVerificationCodeValue": hashlib.sha1("".join(sha1s).encode()).hexdigest()}


def add_sources_spdx(doc, sources, built):
    root = next(r["relatedSpdxElement"] for r in doc["relationships"] if r["relationshipType"] == "DESCRIBES")
    files = {"/" + f.get("fileName", "").lstrip("/"): f for f in doc.get("files", [])}
    for s in sources:
        spdxid = "SPDXRef-Package-built-" + re.sub(r"[^A-Za-z0-9.-]", "-", s["name"])
        contained = [files[f] for f in built[s["name"]] if f in files]
        # SPDX: a package that contains files has them analyzed and carries
        # their verification code.
        analyzed = {"filesAnalyzed": False}
        if contained:
            analyzed = {
                "filesAnalyzed": True,
                "packageVerificationCode": verification_code(contained),
                "licenseInfoFromFiles": ["NOASSERTION"],
            }
        doc["packages"].append(
            {
                "name": s["name"],
                "SPDXID": spdxid,
                "versionInfo": s["version"],
                "downloadLocation": "NOASSERTION",
                **analyzed,
                "licenseConcluded": "NOASSERTION",
                "licenseDeclared": "NOASSERTION",
                "copyrightText": "NOASSERTION",
                "externalRefs": [
                    {"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl", "referenceLocator": s["purl"]}
                ],
            }
        )
        doc["relationships"].append({"spdxElementId": root, "relatedSpdxElement": spdxid, "relationshipType": "CONTAINS"})
        for f in contained:
            doc["relationships"].append(
                {"spdxElementId": spdxid, "relatedSpdxElement": f["SPDXID"], "relationshipType": "CONTAINS"}
            )


def sbom(syft, inputs, ramdisk):
    prefix = re.sub(r"\.cpio\.(zst|gz)$", "", ramdisk)
    with tempfile.TemporaryDirectory() as tmp:
        rootfs = pathlib.Path(tmp).resolve()
        with open_ramdisk(ramdisk) as stream:
            unpack(stream, rootfs)
        # The files this script adds must be new and, through any link on
        # their path (var, var/lib, etc), stay inside the tree.
        for added in (DPKG, "etc/os-release"):
            if os.path.lexists(rootfs / added) or not (rootfs / added).resolve().is_relative_to(rootfs):
                sys.exit(f"ramdisk-sbom: {ramdisk} already has /{added}, or a link on its path leaves the tree")

        packages, digests, owners, frrouting = read_dpkg(inputs)
        sources = read_sources(inputs)
        owned, built, unowned = attribute(rootfs, packages, digests, owners, sources)
        sources = [s for s in sources if built[s["name"]]]
        if owned:
            write_dpkg(rootfs, packages, owned)
            (rootfs / "etc").mkdir(exist_ok=True)
            (rootfs / "etc/os-release").write_bytes((inputs / "os-release").read_bytes())
        print(f"ramdisk-sbom: {ramdisk}: {sum(map(len, owned.values()))} files in {len(owned)} Debian packages, "
              f"built: {', '.join(s['name'] for s in sources) or 'none'}")
        for rel in unowned:
            print(f"ramdisk-sbom: {ramdisk}: no single Debian package ships {rel}")

        env = dict(os.environ, SYFT_CHECK_FOR_APP_UPDATE="false", SYFT_FILE_METADATA_SELECTION="all", SYFT_FILE_METADATA_DIGESTS="sha1,sha256")
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
        fix_cdx(bom, rootfs, frrouting)
    add_sources_cdx(bom, sources, built)
    with open(cdx, "w") as f:
        json.dump(bom, f, indent=2)
    with open(spdx) as f:
        doc = json.load(f)
    fix_spdx(doc, frrouting)
    add_sources_spdx(doc, sources, built)
    with open(spdx, "w") as f:
        json.dump(doc, f, indent=2)


def main(argv):
    if len(argv) < 4:
        sys.exit(__doc__)
    for ramdisk in argv[3:]:
        sbom(argv[1], pathlib.Path(argv[2]), ramdisk)


if __name__ == "__main__":
    main(sys.argv)
