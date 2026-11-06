# X-RAY architecture

```
Storage validation        Structured errors              X-RAY engine            Report        Frontend
recovery.Plan (A1-A7, ->  recovery.ScanError{rule,off}  -> xray.Scan:           -> xray.Report -> /xray page,
scan C0-C8, read-only)    recovery.RefusalError{rule,f}    engine verdict +        (JSON)        Overview counts,
format decoders        -> format.FieldError, dberr      -> byte walk + forward              Recovery/Reliability links
                                                           check + cross-check
```

* `src/internal/recovery/errors.go`: typed errors that wrap the existing
  sentinels (same messages, `errors.Is` unchanged), plus `Plan`, which runs the
  authority and scan decisions read-only without the lock.
* `src/internal/xray`: `scan.go` (orchestration, fingerprints before and after,
  lock probe only if `.lock` exists, engine findings, cross-check, summary),
  `walk.go` (independent byte walk, codes C0-C7, later-frame search),
  `repair.go` (backup, then `kvdb.Open`, `Verify`, rescan),
  `operational.go` (typed API errors to findings), `lab.go` (16 damaged test
  databases).
* Interfaces: `kvdb xray` (CLI, exit codes 0/4/1) and nexus-api `/api/xray/*`
  ([XRAY_API.md](XRAY_API.md)).

**Recovery and X-RAY.** X-RAY never changes recovery behaviour. It predicts
what `kvdb.Open` would do (recoverable, refused, unrecoverable), detects the
one case where the engine's rules would silently drop committed frames
(damaged mid-log header with valid frames after it: CRITICAL, repair refuses),
and calls the real `kvdb.Open` only for an explicit, backed-up repair.
Power-Cut Explorer failures are diagnosed by `xray.Scan` on the kept
pre-recovery image. Findings reference: [XRAY_DIAGNOSTICS.md](XRAY_DIAGNOSTICS.md).
