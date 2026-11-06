package engine

import (
	"testing"

	"github.com/neilmalvalli-cyber/NEXUS_SRINIVAS/torture2/oracle"
)

// The verification counters classify every iteration outcome, count
// acknowledged writes as verified only when the oracle accepted them, and
// keep the seed, iteration and repro command of every failure.
func TestVerificationCounters(t *testing.T) {
	var v Verification
	v.add(&IterResult{Iter: 1, Sent: 10, Acked: 6, AckedWrites: 5, Gets: 2, Rejected: 1, Unknown: 1, Verdict: "PASS", OracleVerdict: "PASS"})
	v.add(&IterResult{Iter: 2, IterSeed: 22, Sent: 4, Acked: 3, AckedWrites: 3, Unknown: 1, Verdict: "FAIL", OracleVerdict: "FAIL",
		FailKind: "partial-batch", Violation: &oracle.Violation{Kind: oracle.VPartialBatch}, Repro: "r2"})
	v.add(&IterResult{Iter: 3, IterSeed: 33, Sent: 4, Acked: 4, AckedWrites: 4, Verdict: "FAIL", OracleVerdict: "FAIL",
		FailKind: "acknowledged-write-lost", Violation: &oracle.Violation{Kind: oracle.VAckedLost}, Repro: "r3"})
	v.add(&IterResult{Iter: 4, IterSeed: 44, Sent: 2, Acked: 2, AckedWrites: 2, Verdict: "FAIL", FailKind: "recovery", Repro: "r4"})
	v.add(&IterResult{Iter: 5, Sent: 3, Acked: 3, AckedWrites: 3, Verdict: "INCONCLUSIVE", OracleVerdict: "INCONCLUSIVE"})
	v.add(&IterResult{Iter: 6, IterSeed: 66, Sent: 1, Verdict: "FAIL", OracleVerdict: "PASS", FailKind: "protocol", Repro: "r6"})

	check := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("attempted", v.OpsAttempted, 24)
	check("acknowledged", v.Acked, 20)
	check("acknowledged writes", v.AckedWrites, 17)
	check("rejected", v.Rejected, 1)
	check("unknown", v.Unknown, 2)
	check("acked writes verified (oracle PASS only)", v.AckedWritesVerif, 5)
	check("reads verified", v.ReadsVerified, 2)
	check("batch atomicity violations", v.BatchViolations, 1)
	check("data integrity violations", v.IntegrityViol, 1)
	check("recovery failures", v.RecoveryFailures, 1)
	check("protocol/liveness failures", v.OtherFailures, 1)
	check("oracle inconclusive", v.OracleInconcl, 1)
	if len(v.Failures) != 4 || v.Failures[0].Iter != 2 || v.Failures[0].IterSeed != 22 || v.Failures[0].Repro != "r2" || v.Failures[3].Kind != "protocol" {
		t.Errorf("failures not recorded with seed, iteration and repro: %+v", v.Failures)
	}
}
