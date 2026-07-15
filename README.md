# FEC-over-transport measurement harness

The harness behind [the write-up](https://man7iss.com/posts/fec-loss-cliff/): it streams for a fixed duration three ways over a controlled network and records delivered goodput, FEC redundancy, and deadline delivery. It runs the comparison the prior FEC-in-QUIC work skipped: a TCP baseline next to QUIC, FEC over QUIC **DATAGRAM** frames (RFC 9221), and the **congestion controller swept as a variable**. Each bulk transfer runs for a fixed number of seconds and reports goodput, so a collapsed controller bounds its own runtime instead of taking minutes to move a fixed payload.

## The three arms

| Arm | Transport | Congestion control | Measures |
|-----|-----------|--------------------|----------|
| `tcp` | one TCP stream | **kernel, swept via sysctl** (cubic, bbr, and BBRv3 if the module is loaded) | the H1/H2/H3 loss-cliff curve |
| `quic` | one QUIC stream | quic-go's own (NewReno-like) | plain-QUIC baseline; deadline (in-order, head-of-line blocking) |
| `quic-fec` | QUIC DATAGRAMs + sliding-window RLC over GF(2^8), paced to the link rate | quic-go's own | FEC vs plain QUIC; redundancy overhead (H5) |

The FEC arm streams source and repair datagrams for the duration; the receiver decodes and reports the post-recovery goodput and the redundancy put on the wire. It is RFC 9265 compliant by construction: quic-go's controller sees the loss of a datagram-carrying packet before the receiver's FEC recovers the payload, so recovery never hides the loss signal.

## Layout

```
fec/        GF(2^8) + sliding-window RLC encoder/decoder   (unit-tested)
stats/      median, coefficient of variation, Jain index   (unit-tested)
netem/      netns + veth + netem command construction       (unit-tested)
transport/  the tcp, quic, and quic-fec arms                (loopback-tested)
harness/    arm dispatch + the netns matrix orchestrator    (parser unit-tested)
cmd/feccliff  server | client | matrix subcommands
scripts/run.sh  build + run the matrix on Linux as root
```

## Build and test (any OS)

```
go test ./...          # fec, stats, netem, and loopback transport tests
go build ./cmd/feccliff
```

The unit tests and the loopback transport tests run anywhere. The loss/RTT matrix needs Linux, because netem, veth, network namespaces, and per-namespace congestion-control sysctls are Linux-only.

## Run the matrix (Linux, root)

On the Hetzner box:

```
git clone https://github.com/man7iss/fec-loss-cliff && cd fec-loss-cliff
sudo ./scripts/run.sh --quick                       # smoke: 3 conditions, cubic + bbr
sudo ./scripts/run.sh                                # full matrix, writes results.csv
sudo ./scripts/run.sh --cc cubic,bbr --duration 10 --reps 5
```

`run.sh` loads `tcp_bbr`, prints the available congestion controllers, builds the binary, clears any stale topology, and runs `feccliff matrix`. Each cell starts a server in the `fecsrv` namespace and drives `--reps` client transfers of `--duration` seconds each from the `feccli` namespace across a netem'd veth pair. Output is a raw per-repetition `results.csv` plus a median/CV summary on stdout. Because bulk transfers are time-bounded, the whole matrix finishes in a predictable wall-clock time regardless of how badly a controller collapses.

### BBRv3

Mainline `tcp_bbr` is BBRv1. For the H3 (BBRv3 at the 2% cliff) row, build the out-of-tree module from [google/bbr](https://github.com/google/bbr) (the `v3` branch) and load it in place of `tcp_bbr`, then pass `--cc cubic,bbr`. The harness reads `tcp_available_congestion_control` and skips any controller that is not loaded, logging the skip rather than failing.

## What maps to which hypothesis

- **H1/H2/H3** (the loss cliff moves with the controller): the `tcp` arm across `sweep-loss{0,0.5,1,2,5}` at 100 Mbit/100 ms, under cubic vs bbr vs bbr3.
- **H5** (bulk FEC costs its redundancy): `quic-fec` `redundancy_pct` on the clean `perfect` cell, and `quic-fec` vs `quic` goodput.
- **H6** (datagram FEC helps deadline traffic): `quic` deadline workload today; the FEC-datagram deadline path is the next arm to wire (see below).

## Deliberately scoped out

- **The FEC deadline arm (`quic-fec` + `--workload deadline`)** is not implemented; the plain-QUIC deadline arm is, so H6 currently compares in-order QUIC against its own clean baseline. The datagram deadline sender/receiver is a direct extension of `serveFECBulk`.
- **The loss-hiding "cheat" mode and the two-flow fairness arm (H4)** need a rate controller we own, because quic-go's controller reacts to datagram-packet loss natively and cannot be told to ignore it. Measuring the cheat therefore requires a raw-UDP arm with a custom AIMD controller and a `CountRecoveredAsLoss` toggle. That is the documented next arm; the compliant case is already covered by construction.

The orchestrator logs every skipped controller and every failed cell.
