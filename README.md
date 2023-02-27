# Ratchet Chat

[![Go Reference](https://pkg.go.dev/badge/git.mills.io/saltyim/ratchet.svg)](https://pkg.go.dev/git.mills.io/saltyim/ratchet)
[![Build Status](https://ci.mills.io/api/badges/saltyim/ratchet/status.svg)](https://ci.mills.io/saltyim/ratchet)
[![REUSE status](https://api.reuse.software/badge/git.mills.io/saltyim/ratchet)](https://api.reuse.software/info/git.mills.io/saltyim/ratchet)

Ratchet is a chat client that utilizes X3DH Double Ratchet protocols over the salty msgbus to ensure E2E encryption
with forward secrecy and self healing properties.

This protocol builds on the infrastructure established by the Salty Protocols.
In particular:

- The EdX25519 key for X3DH, EdDSA, and Curve25519 signatures.
- The MsgBus inbox delivery.
- Salty autodiscovery URLs.
- Salty message and event format for chat content.

Find the full spec here: https://salty.im/spec.html

## Binary Protocol

All messages are wrapped with the following envelope.

```
  "!RAT!" | '1' - '5' | ... | "!CHT!"
```

A prefix/suffix of `!RAT!` `!CHT!` and a byte that indicates message type. The remaining bytes are URL Safe Base64 without padding.


### Offer Message

```

Message Type = '1'

| 0 .. 31 | 32 .. 63 | 64 .. 127 | 128 .. 143 | 144 .. + |
| Pubkey  | SP Key   | SP Sig    | Session ID | Nick     |
```

Pubkey
: Public ed25519 key of offering party. (Ack party verifies against salty discovery pubkey.)

SP Kkey
: The X25519 signed prekey

SP Sig
: The signature for X25519 signed prekey

Session ID
: A random ULID used by the offering party to identify the session

Nick
: The nickname of the offering party using (see salty lookup for usage)


### Ack Message

```

Message Type = '2'

| 0 .. 31 | 32 .. 63 | 64 .. 79  | 80 .. 179 |
| Pubkey  | E Key    | SessionID | Encrypted |

Encrypted Payload
| 0 .. 15   | 16 .. 100 |
| SessionID | Random    |

```

Pubkey
: Public ed25519 key of acking party. (Offering party verified against salty discovery pubkey.)
E Kkey

: The X25519 Ephemeral key of the acking party.

Session ID
: The ULID used by the offering party to identify the session.

Encrypted
: Initial payload with contents below

Encrypted Payload:

SessionID
: Random ULID used by ack party to identify the session.

Random
: Random bytes to fill the rest of payload

### Data Message

```

Message Type = '3'

| 0 .. 15   | 16 .. +   |
| SessionID | Encrypted |

```

SessionID
: Random ULID used by receiving party to identify the session.

Encrypted
: Payload data that is decoded f

### Close Message

```

Message Type = '4'

| 0 .. 15   | 16   |
| SessionID | 0xFF |

```

SessionID
: Random ULID used by receiving party to identify the session.

0xFF
: Encrypted value that matches the value 0xFF when decrypted.


### Sealed Message

Sealed messages offer additonal privacy around the offer message to protect interception by other actors. The
parameters are safe to be exposed as they utilize EdDSA to prevent exposing the secret values. The encryption
is to protect the offer party nick from being exposed.

```

Message Type = '5'

| 0 .. 31  | 32 .. +   |
| E Pubkey | Encrypted |

Encrypted Payload

| 0    | 1 .. +         |
| Type | Message Content|

```

E Pubkey
: Ephemeral Pubkey used to seal using the nacl anonymous box algorithem.

Encrypted
: Encrypted payload

Encrypted Payload

Type
: Message type of content

Message Content
: Content as indicated by type


## Xochimilco

An implementation of the [Signal Protocols][signal-docs] [X3DH][signal-x3dh] and [Double Ratchet][signal-double-ratchet].
Plus a simple straightforward usable E2E encryption library build on top, named Xochimilco.

For both implementation details and examples, take a look at the [documentation][go-doc].

Some background, the [lake Xochimilco][wiki-xochimilco] seems to be the last native habitat for the [axolotl][wiki-axolotl].
This salamander, also called _Mexican walking fish_, has incredibly self healing abilities.
For this reason, the Double Ratchet algorithm was initially named after this animal.

[go-doc]: https://pkg.go.dev/git.mills.io/saltyim/ratchet
[signal-docs]: https://signal.org/docs/
[signal-x3dh]: https://signal.org/docs/specifications/x3dh/
[signal-double-ratchet]: https://signal.org/docs/specifications/doubleratchet/
[wiki-axolotl]: https://en.wikipedia.org/wiki/Axolotl
[wiki-xochimilco]: https://en.wikipedia.org/wiki/Lake_Xochimilco
