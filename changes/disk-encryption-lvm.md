---
section: Upgrade notes for CAPRF
---

- `DISK_ENCRYPTION=luks2-tpm2` supports images with the root on LVM
  (#945): BOOTy formats the physical volume of VG `tcaas` with LUKS2 and
  writes it into the opened volume, so no LVM data reaches the disk in
  plain text. The image needs `lvm2`, the ramdisk `lvm`. The kernel
  command line gets `rd.luks.uuid=<luks uuid> rd.lvm.vg=tcaas`; recovery
  key escrow, the PCR 7+14 enrollment and the locked keys are unchanged.
  The logical volumes keep the sizes of the image; the space of the grown
  disk stays free in VG `tcaas`.
