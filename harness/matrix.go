package harness

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"feccliff/netem"
	"feccliff/stats"
	"feccliff/transport"
)

// ArmSpec pairs an arm with the kernel congestion controller to run it under.
// CC is empty for the QUIC arms, whose controller is quic-go's own.
type ArmSpec struct {
	Arm string
	CC  string
}

// Row is one measured repetition, the unit written to the raw CSV.
type Row struct {
	Condition     string
	Arm           string
	CC            string
	Workload      string
	Rep           int
	GoodputMbit   float64
	DurationMs    float64
	RedundancyPct float64
	OnTimeFrac    float64
	WireBytes     int64
}

// Config parameterizes a matrix run.
type Config struct {
	Port      string
	Reps      int
	Duration  time.Duration
	BulkBytes int64
	OutPath   string
	Quick     bool
	CCs       []string
}

// Run executes the full matrix: for each network condition it applies netem,
// then for each arm and congestion controller it starts a server in the server
// namespace and drives Reps client transfers from the client namespace. It
// writes every repetition to the raw CSV and prints a median summary. It must
// run as root on Linux; the netns and tc commands require it.
func Run(cfg Config) error {
	var rows []Row

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self: %w", err)
	}
	conds := defaultConditions(cfg.Quick)
	work := Bulk(cfg.Duration, cfg.BulkBytes)
	for _, c := range conds {
		netem.Teardown()
		if serr := c.Setup(); serr != nil {
			return serr
		}
		avail := availableCCs()
		for _, spec := range armSpecs(cfg.CCs) {
			if spec.Arm == transport.ArmTCP && !contains(avail, spec.CC) {
				fmt.Printf("skip %s/%s under %s: congestion control %q not available (module not loaded)\n", spec.Arm, spec.CC, c.Name, spec.CC)
				continue
			}
			rows = append(rows, runArmCell(self, spec, c, work, cfg)...)
		}
		netem.Teardown()
	}
	if werr := writeCSV(cfg.OutPath, rows); werr != nil {
		return werr
	}
	printSummary(rows)
	return nil
}

func runArmCell(self string, spec ArmSpec, c netem.Condition, work Workload, cfg Config) []Row {
	var rows []Row

	addr := netem.ServerIP + ":" + cfg.Port
	if spec.Arm == transport.ArmTCP {
		setCC(spec.CC)
	}
	srv := startServer(self, spec.Arm, addr)
	if srv == nil {
		return rows
	}
	defer stopServer(srv)
	time.Sleep(700 * time.Millisecond)
	for rep := 0; rep < cfg.Reps; rep++ {
		res, err := runClient(self, spec.Arm, addr, work, c.RateMbit)
		if err != nil {
			fmt.Printf("client %s/%s under %s rep %d failed: %v\n", spec.Arm, spec.CC, c.Name, rep, err)
			continue
		}
		rows = append(rows, Row{
			Condition:     c.Name,
			Arm:           spec.Arm,
			CC:            armCC(spec),
			Workload:      work.Kind,
			Rep:           rep,
			GoodputMbit:   res.GoodputMbit(),
			DurationMs:    float64(res.Duration.Microseconds()) / 1000,
			RedundancyPct: res.RedundancyPct(),
			OnTimeFrac:    res.OnTimeFrac(),
			WireBytes:     res.WireBytes,
		})
	}
	return rows
}

func armCC(spec ArmSpec) string {
	if spec.CC == "" {
		return "quic-default"
	}
	return spec.CC
}

func startServer(self, arm, addr string) *exec.Cmd {
	cmd := nsExec(netem.NSServer, self, "server", "--arm", arm, "--addr", addr)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Printf("start server %s failed: %v\n", arm, err)
		return nil
	}
	return cmd
}

func stopServer(cmd *exec.Cmd) {
	cmd.Process.Kill()
	cmd.Wait()
}

func runClient(self, arm, addr string, work Workload, rateMbit int) (transport.Result, error) {
	var res transport.Result

	ctx, cancel := context.WithTimeout(context.Background(), work.Duration+60*time.Second)
	defer cancel()
	args := []string{
		"netns", "exec", netem.NSClient, self, "client",
		"--arm", arm, "--addr", addr, "--workload", work.Kind,
		"--duration", strconv.Itoa(int(work.Duration / time.Second)),
		"--bytes", strconv.FormatInt(work.MaxBytes, 10),
		"--rate", strconv.Itoa(rateMbit),
	}
	out, err := exec.CommandContext(ctx, "ip", args...).Output()
	if err != nil {
		return res, fmt.Errorf("client exec: %w", err)
	}
	return parseResult(string(out))
}

// parseResult reads the RESULT line the client subcommand prints.
func parseResult(out string) (transport.Result, error) {
	var res transport.Result

	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "RESULT ") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "RESULT "))
		vals := make(map[string]string, len(fields))
		for _, f := range fields {
			kv := strings.SplitN(f, "=", 2)
			if len(kv) == 2 {
				vals[kv[0]] = kv[1]
			}
		}
		res.PayloadBytes = atoiOr(vals["payload_bytes"], 0)
		res.WireBytes = atoiOr(vals["wire_bytes"], 0)
		res.RepairBytes = atoiOr(vals["repair_bytes"], 0)
		res.Duration = time.Duration(atoiOr(vals["duration_ns"], 0))
		res.OnTime = int(atoiOr(vals["on_time"], 0))
		res.Total = int(atoiOr(vals["total"], 0))
		return res, nil
	}
	return res, fmt.Errorf("no RESULT line in client output")
}

func atoiOr(s string, def int64) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

func nsExec(ns, self string, args ...string) *exec.Cmd {
	full := append([]string{"netns", "exec", ns, self}, args...)
	return exec.Command("ip", full...)
}

func setCC(cc string) {
	for _, ns := range []string{netem.NSClient, netem.NSServer} {
		exec.Command("ip", "netns", "exec", ns, "sysctl", "-wq", "net.ipv4.tcp_congestion_control="+cc).Run()
	}
}

func availableCCs() []string {
	out, err := exec.Command("ip", "netns", "exec", netem.NSClient, "sysctl", "-n", "net.ipv4.tcp_available_congestion_control").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func writeCSV(path string, rows []Row) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"condition", "arm", "cc", "workload", "rep", "goodput_mbit", "duration_ms", "redundancy_pct", "ontime_frac", "wire_bytes"})
	for _, r := range rows {
		w.Write([]string{
			r.Condition, r.Arm, r.CC, r.Workload, strconv.Itoa(r.Rep),
			strconv.FormatFloat(r.GoodputMbit, 'f', 3, 64),
			strconv.FormatFloat(r.DurationMs, 'f', 3, 64),
			strconv.FormatFloat(r.RedundancyPct, 'f', 3, 64),
			strconv.FormatFloat(r.OnTimeFrac, 'f', 4, 64),
			strconv.FormatInt(r.WireBytes, 10),
		})
	}
	return nil
}

// printSummary aggregates rows to a median-and-CV table per condition/arm/cc.
func printSummary(rows []Row) {
	type key struct{ cond, arm, cc string }
	groups := map[key][]float64{}
	order := []key{}
	for _, r := range rows {
		k := key{r.Condition, r.Arm, r.CC}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r.GoodputMbit)
	}
	fmt.Printf("\n%-16s %-9s %-8s %12s %8s\n", "condition", "arm", "cc", "goodput", "cv%")
	for _, k := range order {
		g := groups[k]
		fmt.Printf("%-16s %-9s %-8s %10.1f m %7.1f\n", k.cond, k.arm, k.cc, stats.Median(g), stats.CV(g))
	}
}

func defaultConditions(quick bool) []netem.Condition {
	var conds []netem.Condition

	for _, loss := range []float64{0, 0.5, 1, 2, 5} {
		conds = append(conds, netem.Condition{
			Name: fmt.Sprintf("sweep-loss%.3g", loss), RateMbit: 100, RTTMs: 100, LossPct: loss,
		})
	}
	conds = append(conds,
		netem.Condition{Name: "perfect", RateMbit: 1000, RTTMs: 2},
		netem.Condition{Name: "good", RateMbit: 200, RTTMs: 25, LossPct: 0.1},
		netem.Condition{Name: "bad", RateMbit: 50, RTTMs: 80, JitterMs: 20, LossPct: 2},
		netem.Condition{Name: "broken", RateMbit: 10, RTTMs: 200, JitterMs: 50, LossPct: 10, ReorderPct: 5, DupPct: 1},
	)
	if quick {
		return []netem.Condition{conds[0], conds[3], conds[8]}
	}
	return conds
}

func armSpecs(ccs []string) []ArmSpec {
	var specs []ArmSpec

	for _, cc := range ccs {
		specs = append(specs, ArmSpec{Arm: transport.ArmTCP, CC: cc})
	}
	specs = append(specs, ArmSpec{Arm: transport.ArmQUIC}, ArmSpec{Arm: transport.ArmQUICFEC})
	return specs
}
