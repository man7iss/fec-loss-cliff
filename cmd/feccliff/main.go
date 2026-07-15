// Command feccliff drives the FEC-over-transport measurement harness. The
// server and client subcommands run inside the two network namespaces; the
// matrix subcommand orchestrates the full loss/RTT/congestion sweep.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"feccliff/harness"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "server":
		cmdServer(os.Args[2:])
	case "client":
		cmdClient(os.Args[2:])
	case "matrix":
		cmdMatrix(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: feccliff <server|client|matrix> [flags]")
	fmt.Fprintln(os.Stderr, "  server  run an arm's server (inside the server netns)")
	fmt.Fprintln(os.Stderr, "  client  run one transfer and print a RESULT line (inside the client netns)")
	fmt.Fprintln(os.Stderr, "  matrix  orchestrate the full sweep (root, Linux)")
}

func cmdServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	arm := fs.String("arm", "tcp", "arm: tcp | quic | quic-fec")
	addr := fs.String("addr", ":8443", "listen address")
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := harness.ServeArm(ctx, *arm, *addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cmdClient(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	arm := fs.String("arm", "tcp", "arm: tcp | quic | quic-fec")
	addr := fs.String("addr", "", "server address")
	workload := fs.String("workload", "bulk", "workload: bulk | deadline")
	duration := fs.Int("duration", 12, "bulk: seconds to stream")
	bytes := fs.Int64("bytes", 0, "bulk: byte cap (0 = no cap)")
	symbol := fs.Int("symbol", 1000, "FEC symbol size")
	repair := fs.Int("repair-permille", 200, "FEC repair symbols per thousand source symbols")
	rate := fs.Int("rate", 0, "FEC pacing rate in Mbit/s (0 = unpaced)")
	numADU := fs.Int("adus", 300, "deadline: number of application data units")
	aduBytes := fs.Int("adu-bytes", 1000, "deadline: bytes per unit")
	periodMs := fs.Int("period-ms", 16, "deadline: milliseconds between units")
	deadlineMs := fs.Int("deadline-ms", 250, "deadline: milliseconds allowed per unit")
	fs.Parse(args)

	var work harness.Workload
	if *workload == "deadline" {
		work = harness.Deadline(*numADU, *aduBytes, time.Duration(*periodMs)*time.Millisecond, time.Duration(*deadlineMs)*time.Millisecond)
	} else {
		work = harness.Bulk(time.Duration(*duration)*time.Second, *bytes)
		work.SymbolSize = *symbol
		work.RepairPerMille = *repair
		work.RateMbit = *rate
	}
	res, err := harness.RunArm(*arm, *addr, work)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("RESULT payload_bytes=%d wire_bytes=%d repair_bytes=%d duration_ns=%d on_time=%d total=%d\n",
		res.PayloadBytes, res.WireBytes, res.RepairBytes, res.Duration.Nanoseconds(), res.OnTime, res.Total)
}

func cmdMatrix(args []string) {
	fs := flag.NewFlagSet("matrix", flag.ExitOnError)
	reps := fs.Int("reps", 5, "repetitions per cell")
	duration := fs.Int("duration", 12, "seconds to stream per transfer")
	bytes := fs.Int64("bytes", 0, "bulk byte cap (0 = no cap)")
	port := fs.String("port", "8443", "server port")
	out := fs.String("out", "results.csv", "raw CSV output path")
	quick := fs.Bool("quick", false, "run a reduced condition set")
	ccList := fs.String("cc", "cubic,bbr", "comma-separated kernel congestion controllers for the TCP arm")
	fs.Parse(args)

	cfg := harness.Config{
		Port:      *port,
		Reps:      *reps,
		Duration:  time.Duration(*duration) * time.Second,
		BulkBytes: *bytes,
		OutPath:   *out,
		Quick:     *quick,
		CCs:       strings.Split(*ccList, ","),
	}
	if err := harness.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
