/* Hardened compile-time options for the rescue SSH server (dropbear).
 * initrd.Dockerfile copies this file into the dropbear source tree unless
 * the build runs with DROPBEAR_HARDENED=false. Options set here override
 * default_options.h. docs/configuration.md#rescue lists what remains. */

/* Key exchange: curve25519, ECDH (NIST P-256/384/521) and the hybrid
 * post-quantum methods stay. Finite-field Diffie-Hellman goes. */
#define DROPBEAR_DH_GROUP14_SHA256 0
#define DROPBEAR_DH_GROUP14_SHA1 0
#define DROPBEAR_DH_GROUP16 0
#define DROPBEAR_DH_GROUP1 0

/* Host and user keys: Ed25519, ECDSA and FIDO (sk-) keys. No RSA, no DSA. */
#define DROPBEAR_RSA 0
#define DROPBEAR_DSS 0

/* MACs: hmac-sha2-256 (and the AEAD chacha20-poly1305). No SHA-1. */
#define DROPBEAR_SHA1_HMAC 0
#define DROPBEAR_SHA1_96_HMAC 0

/* The ramdisk has no sftp-server; refuse the subsystem outright. */
#define DROPBEAR_SFTPSERVER 0

/* No agent or Unix socket forwarding. TCP forwarding stays compiled in so
 * that RESCUE_SSH_HARDENED=false can still allow it; BOOTy starts dropbear
 * with -j -k by default. */
#define DROPBEAR_SVR_AGENTFWD 0
#define DROPBEAR_SVR_LOCALSTREAMFWD 0
#define DROPBEAR_X11FWD 0
